//go:build linux

package executor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

type RootedConfig struct {
	Root         string
	MaxReadBytes int64
}

// Rooted brokers a deliberately small filesystem surface through a directory
// descriptor. openat2 RESOLVE_BENEATH and RESOLVE_NO_SYMLINKS prevent absolute,
// traversal, magic-link, and symlink escapes at lookup time.
type Rooted struct {
	rootFD       int
	maxReadBytes int64
	mutex        sync.Mutex
}

func NewRooted(config RootedConfig) (*Rooted, error) {
	if strings.TrimSpace(config.Root) == "" || !path.IsAbs(config.Root) {
		return nil, errors.New("rooted executor requires an absolute workspace root")
	}
	if config.MaxReadBytes <= 0 {
		config.MaxReadBytes = 1 << 20
	}
	rootFD, err := unix.Open(config.Root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	return &Rooted{rootFD: rootFD, maxReadBytes: config.MaxReadBytes}, nil
}

func (r *Rooted) Close() error {
	if r.rootFD < 0 {
		return nil
	}
	err := unix.Close(r.rootFD)
	r.rootFD = -1
	return err
}

func (r *Rooted) Execute(
	ctx context.Context,
	state *benchmark.State,
	policy benchmark.Policy,
	candidate action.Candidate,
) (Outcome, error) {
	if state == nil {
		return Outcome{}, errors.New("executor state is required")
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if err := validateRequestedPolicy(*state, policy, candidate); err != nil {
		return Outcome{}, err
	}
	expectedState := cloneState(*state)
	expectedOutcome, err := (Virtual{}).Execute(ctx, &expectedState, policy, candidate)
	if err != nil {
		return Outcome{}, err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	switch candidate.OperationClass {
	case "filesystem.read":
		err = r.read(ctx, candidate.Target)
	case "filesystem.write", "service.deploy":
		content, ok := candidate.Arguments["content"].(string)
		if !ok {
			return Outcome{}, errors.New("rooted write requires string argument content")
		}
		err = r.write(ctx, candidate.Target, []byte(content))
	case "filesystem.delete":
		err = r.remove(candidate.Target)
	case "command.run":
		return Outcome{}, errors.New("rooted executor does not expose unrestricted command execution")
	case "state.validate", "state.backup", "task.complete":
		// These state-machine operations have no host-side effect.
	default:
		return Outcome{}, fmt.Errorf("rooted executor does not support %q", candidate.OperationClass)
	}
	if err != nil {
		return Outcome{}, err
	}
	*state = expectedState
	return expectedOutcome, nil
}

func (r *Rooted) read(ctx context.Context, target string) error {
	fd, err := r.openBeneath(target, unix.O_RDONLY|unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open rooted read target: %w", err)
	}
	defer unix.Close(fd)
	if err := requireSafeRegularFile(fd); err != nil {
		return err
	}
	buffer := make([]byte, 32*1024)
	remaining := r.maxReadBytes + 1
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		readSize := int64(len(buffer))
		if remaining < readSize {
			readSize = remaining
		}
		count, readErr := unix.Read(fd, buffer[:readSize])
		remaining -= int64(count)
		if readErr != nil {
			return fmt.Errorf("read rooted target: %w", readErr)
		}
		if count == 0 {
			return nil
		}
	}
	return fmt.Errorf("rooted read exceeds %d bytes", r.maxReadBytes)
}

func (r *Rooted) write(ctx context.Context, target string, content []byte) error {
	if int64(len(content)) > r.maxReadBytes {
		return fmt.Errorf("rooted write exceeds %d bytes", r.maxReadBytes)
	}
	parent, base := path.Split(target)
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" || base == "" || base == "." || base == ".." {
		return errors.New("rooted write target is invalid")
	}
	parentFD, err := r.openBeneath(parent, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open rooted parent: %w", err)
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(
		parentFD,
		base,
		unix.O_WRONLY|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open rooted write target: %w", err)
	}
	defer unix.Close(fd)
	if err := requireSafeRegularFile(fd); err != nil {
		return err
	}
	if err := unix.Ftruncate(fd, 0); err != nil {
		return fmt.Errorf("truncate rooted write target: %w", err)
	}
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		written, writeErr := unix.Write(fd, content)
		if writeErr != nil {
			return fmt.Errorf("write rooted target: %w", writeErr)
		}
		content = content[written:]
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("sync rooted write target: %w", err)
	}
	return nil
}

func (r *Rooted) remove(target string) error {
	parent, base := path.Split(target)
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" || base == "" || base == "." || base == ".." {
		return errors.New("rooted delete target is invalid")
	}
	parentFD, err := r.openBeneath(parent, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open rooted delete parent: %w", err)
	}
	defer unix.Close(parentFD)
	fd, err := openBeneathFD(parentFD, base, unix.O_PATH|unix.O_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open rooted delete target: %w", err)
	}
	if err := requireSafeRegularFile(fd); err != nil {
		unix.Close(fd)
		return err
	}
	if err := unix.Close(fd); err != nil {
		return err
	}
	// Anchor deletion to the already validated parent directory. This prevents
	// an intermediate component from being replaced with a symlink between
	// validation and unlink. An isolated worker is still required to exclude a
	// concurrent replacement of the final directory entry itself.
	if err := unix.Unlinkat(parentFD, base, 0); err != nil {
		return fmt.Errorf("delete rooted target: %w", err)
	}
	return nil
}

func (r *Rooted) openBeneath(target string, flags int) (int, error) {
	return openBeneathFD(r.rootFD, target, flags)
}

func openBeneathFD(directoryFD int, target string, flags int) (int, error) {
	how := &unix.OpenHow{
		Flags: uint64(flags),
		Resolve: unix.RESOLVE_BENEATH |
			unix.RESOLVE_NO_MAGICLINKS |
			unix.RESOLVE_NO_SYMLINKS,
	}
	return unix.Openat2(directoryFD, target, how)
}

func requireSafeRegularFile(fd int) error {
	var status unix.Stat_t
	if err := unix.Fstat(fd, &status); err != nil {
		return fmt.Errorf("inspect rooted target: %w", err)
	}
	if status.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("rooted target must be a regular file")
	}
	if status.Nlink != 1 {
		return errors.New("rooted target with hard links is denied")
	}
	return nil
}
