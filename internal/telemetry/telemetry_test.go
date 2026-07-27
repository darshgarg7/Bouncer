package telemetry

import (
	"context"
	"testing"
)

func TestSetupNoopAndValidation(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{ServiceName: "test", ServiceVersion: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{
		{},
		{ServiceName: "test", SampleRatio: 2},
		{ServiceName: "test", OTLPEndpoint: "not-a-url"},
	} {
		if _, err := Setup(context.Background(), config); err == nil {
			t.Fatalf("Setup accepted %+v", config)
		}
	}
}
