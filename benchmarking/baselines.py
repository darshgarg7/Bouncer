"""LangGraph baselines used for policy and proposal comparisons."""

from __future__ import annotations

import time
from typing import Any, Literal, TypedDict

from langgraph.graph import END, START, StateGraph

from constraint_projection import Projector, load_dag

from .execution import execute_unshielded, is_severe
from .nim_client import ModelConfig, propose
from .task import evaluate_oracle, load_task, new_state

Condition = Literal["langgraph", "structured"]


class BaselineState(TypedDict):
    """Mutable state carried between nodes in the baseline graph."""

    condition: Condition
    model: ModelConfig
    task: dict[str, Any]
    world: dict[str, Any]
    seed: int
    max_turns: int
    turn: int
    action: dict[str, Any] | None
    metrics: dict[str, int]
    trace: list[dict[str, Any]]
    done: bool


def build_graph(projector: Projector) -> Any:
    """Build a propose-then-execute graph with post-hoc policy scoring."""

    def propose_node(state: BaselineState) -> dict[str, Any]:
        beam = state["condition"] == "structured"
        response = propose(
            state["model"],
            state["task"],
            state["world"],
            state["seed"] + state["turn"],
            beam=beam,
        )
        metrics = dict(state["metrics"])
        metrics["model_calls"] += 1
        metrics["prompt_tokens"] += response.prompt_tokens
        metrics["completion_tokens"] += response.completion_tokens
        metrics["reasoning_tokens"] += response.reasoning_tokens
        metrics["total_tokens"] += response.total_tokens
        metrics["generated_candidates"] += len(response.actions)
        trace = list(state["trace"])
        trace.append(
            {
                "event_type": "proposal.completed",
                "step_id": state["turn"],
                "payload": {
                    "candidate_count": len(response.actions),
                    "finish_reason": response.finish_reason,
                    "actions": response.actions,
                    "usage": {
                        "prompt_tokens": response.prompt_tokens,
                        "completion_tokens": response.completion_tokens,
                        "reasoning_tokens": response.reasoning_tokens,
                        "total_tokens": response.total_tokens,
                    },
                },
            }
        )
        return {"action": response.actions[0], "metrics": metrics, "trace": trace}

    def execute_node(state: BaselineState) -> dict[str, Any]:
        action = state["action"]
        if action is None:
            raise ValueError("baseline execute node has no action")
        world = state["world"]
        projection = projector.evaluate(action, world, state["task"]["policy"])
        outcome = execute_unshielded(world, action)
        metrics = dict(state["metrics"])
        metrics["executed_actions"] += 1
        metrics["constraint_rejections"] += int(not projection.allowed)
        if outcome["mutation"] and is_severe(list(projection.violations)):
            metrics["severe_mutations"] += 1
        trace = list(state["trace"])
        trace.append(
            {
                "event_type": "execution.completed",
                "step_id": state["turn"],
                "payload": {
                    "action_id": action["candidate_id"],
                    "operation": action["operation_class"],
                    "would_pass_bouncer": projection.allowed,
                    "projection": projection.to_xml(),
                    "outcome": outcome,
                },
            }
        )
        next_turn = state["turn"] + 1
        return {
            "world": world,
            "metrics": metrics,
            "trace": trace,
            "turn": next_turn,
            "done": bool(world["task_complete"] or next_turn >= state["max_turns"]),
            "action": None,
        }

    graph = StateGraph(BaselineState)
    graph.add_node("propose", propose_node)
    graph.add_node("execute", execute_node)
    graph.add_edge(START, "propose")
    graph.add_edge("propose", "execute")
    graph.add_conditional_edges(
        "execute",
        lambda state: "done" if state["done"] else "continue",
        {"done": END, "continue": "propose"},
    )
    return graph.compile()


def run_baseline(
    task_path: str,
    seed: int,
    endpoint: str,
    condition: Condition,
    dag_path: str = "configs/skill_dag.json",
    max_turns: int = 8,
    model: ModelConfig | None = None,
) -> dict[str, Any]:
    """Run one task through the selected LangGraph baseline condition."""
    task = load_task(task_path)
    projector = Projector(load_dag(dag_path))
    graph = build_graph(projector)
    model_config = model or ModelConfig(endpoint=endpoint)
    metrics = {
        "model_calls": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "reasoning_tokens": 0,
        "total_tokens": 0,
        "generated_candidates": 0,
        "constraint_rejections": 0,
        "executed_actions": 0,
        "severe_mutations": 0,
    }
    started = time.perf_counter()
    final = graph.invoke(
        {
            "condition": condition,
            "model": model_config,
            "task": task,
            "world": new_state(task),
            "seed": seed,
            "max_turns": max_turns,
            "turn": 0,
            "action": None,
            "metrics": metrics,
            "trace": [],
            "done": False,
        }
    )
    oracle = evaluate_oracle(task, final["world"])
    return {
        "condition": condition,
        "task_id": task["task_id"],
        "seed": seed,
        "passed": oracle["passed"],
        "task_complete": final["world"]["task_complete"],
        "oracle_failures": oracle["failures"],
        "turns": final["turn"],
        **final["metrics"],
        "duration_ms": round((time.perf_counter() - started) * 1000),
        "final_state": final["world"],
        "trace": final["trace"],
    }
