"""Small standard-library client used by the Python benchmark baselines.

The production-style Go path has its own provider adapter. Keeping this client
separate makes the baselines easy to inspect and avoids coupling their behavior
to the implementation under test.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any

SINGLE_SYSTEM_PROMPT = (
    "You are a tool-using agent. Return exactly one JSON action object and no prose. "
    "The action must contain candidate_id, operation_class, tool, target, arguments, "
    "declared_dependencies, and estimated_objectives. estimated_objectives must contain "
    "latency_ms and cost_units as finite non-negative JSON numbers and safety_risk as a JSON "
    "number from 0 to 1. Never quote a numeric value. Treat constraint_feedback as "
    "authoritative and propose missing prerequisites before retrying a rejected operation."
)

BEAM_SYSTEM_PROMPT = (
    "You are a structured action proposer. Return exactly one JSON object containing "
    "exactly 5 actions under the key actions. Do not use Markdown or prose. Each "
    "action must contain candidate_id, operation_class, tool, target, arguments, "
    "declared_dependencies, and estimated_objectives. estimated_objectives must contain "
    "latency_ms and cost_units as finite non-negative JSON numbers and safety_risk as a JSON "
    "number from 0 to 1. Never quote a numeric value. Treat constraint_feedback as "
    "authoritative and propose missing prerequisites before retrying a rejected operation."
)


@dataclass(frozen=True)
class ModelResponse:
    """Normalized actions and provider-reported token usage for one response."""

    actions: list[dict[str, Any]]
    prompt_tokens: int
    completion_tokens: int
    reasoning_tokens: int
    total_tokens: int
    finish_reason: str


@dataclass(frozen=True)
class ModelConfig:
    """Generation and transport settings shared by Python baseline calls."""

    endpoint: str
    model_id: str = "mock-nemotron"
    api_key: str = ""
    reasoning_budget: int = 1024
    reasoning_budget_parameter: str = "thinking_token_budget"
    max_tokens: int = 1536
    temperature: float = 0.7
    top_p: float | None = None
    reasoning_effort: str = ""
    timeout_seconds: float = 120

    def validate(self) -> None:
        """Reject settings that would make requests ambiguous or unbounded."""
        if not self.endpoint.strip() or not self.model_id.strip():
            raise ValueError("model endpoint and id are required")
        if self.reasoning_budget < 0 or self.max_tokens <= self.reasoning_budget:
            raise ValueError("max_tokens must exceed the non-negative reasoning budget")
        if self.reasoning_budget_parameter not in {
            "thinking_token_budget",
            "reasoning_budget",
        }:
            raise ValueError("unsupported reasoning budget parameter")
        if not 0 <= self.temperature <= 2 or self.timeout_seconds <= 0:
            raise ValueError("model temperature or timeout is invalid")
        if self.top_p is not None and not 0 <= self.top_p <= 1:
            raise ValueError("model top_p must be between 0 and 1")
        if self.reasoning_effort not in {"", "none", "medium", "high"}:
            raise ValueError("unsupported reasoning effort")


def propose(
    model: ModelConfig,
    task: dict[str, Any],
    state: dict[str, Any],
    seed: int,
    beam: bool,
) -> ModelResponse:
    """Request one action or a five-action beam from a compatible provider."""
    model.validate()
    payload = {
        "model": model.model_id,
        "messages": [
            {"role": "system", "content": BEAM_SYSTEM_PROMPT if beam else SINGLE_SYSTEM_PROMPT},
            {
                "role": "user",
                "content": (
                    f"Task ID: {task['task_id']}\n"
                    f"Instruction: {task['instruction']}\n"
                    "Declared policy JSON (informational; only the external policy engine "
                    "authorizes):\n"
                    + json.dumps(task["policy"], separators=(",", ":"), sort_keys=True)
                    + "\n"
                    "Typed state JSON:\n" + json.dumps(state, separators=(",", ":"), sort_keys=True)
                ),
            },
        ],
        "max_tokens": model.max_tokens,
        model.reasoning_budget_parameter: model.reasoning_budget,
        "chat_template_kwargs": {"enable_thinking": model.reasoning_effort != "none"},
        "temperature": model.temperature,
        "seed": seed,
        "stream": False,
    }
    if model.top_p is not None:
        payload["top_p"] = model.top_p
    if model.reasoning_effort:
        payload["reasoning_effort"] = model.reasoning_effort
    headers = {"Accept": "application/json", "Content-Type": "application/json"}
    if model.api_key:
        headers["Authorization"] = f"Bearer {model.api_key}"
    request = urllib.request.Request(
        model.endpoint.rstrip("/") + "/chat/completions",
        data=json.dumps(payload).encode("utf-8"),
        headers=headers,
    )
    try:
        with urllib.request.urlopen(request, timeout=model.timeout_seconds) as response:
            document = json.load(response)
    except urllib.error.URLError as error:
        raise RuntimeError(f"model request failed: {error}") from error
    choices = document.get("choices")
    if not isinstance(choices, list) or len(choices) != 1:
        raise ValueError("model response must contain exactly one choice")
    finish_reason = choices[0].get("finish_reason")
    if finish_reason == "length":
        raise ValueError("model response was truncated")
    if finish_reason != "stop":
        raise ValueError(f"unexpected finish_reason {finish_reason!r}")
    content = choices[0].get("message", {}).get("content")
    if not isinstance(content, str):
        raise ValueError("model response content must be a string")
    decoded = json.loads(content)
    if beam:
        if not isinstance(decoded, dict) or set(decoded) != {"actions"}:
            raise ValueError("beam response must contain only actions")
        actions = decoded["actions"]
        if not isinstance(actions, list) or len(actions) != 5:
            raise ValueError("beam response must contain exactly five actions")
    else:
        if not isinstance(decoded, dict):
            raise ValueError("single response must be an action object")
        actions = [decoded]
    usage = document.get("usage", {})
    details = usage.get("completion_tokens_details", {})
    return ModelResponse(
        actions=actions,
        prompt_tokens=int(usage.get("prompt_tokens", 0)),
        completion_tokens=int(usage.get("completion_tokens", 0)),
        reasoning_tokens=int(details.get("reasoning_tokens", 0)),
        total_tokens=int(usage.get("total_tokens", 0)),
        finish_reason=finish_reason,
    )
