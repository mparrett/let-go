"""Model backends for the docs runner.

Two real clients behind one interface rather than a single OpenAI-compatible
path pointed at both: Anthropic's own SDK is the supported way to reach Claude,
and its compatibility shim gives up thinking, effort and refusal handling.
DigitalOcean's GenAI endpoints really are OpenAI-shaped, so that backend uses
the OpenAI SDK for what it actually is.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import ClassVar, Protocol


@dataclass
class Usage:
    """Token counts accumulated across every model call in one run.

    A run makes one call per page it edits plus one per new-page decision, so
    the per-call number is not the interesting one; the run total is, because
    that is what a cost estimate is built from.

    `calls_without_usage` exists so a backend that *cannot* report tokens is
    distinguishable from one that used none. Reporting `in=0` for the CLI
    backend would be a lie shaped exactly like a measurement, and an evaluation
    comparing models on cost would silently believe it.
    """

    calls: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    calls_without_usage: int = 0

    def record(self, input_tokens: int | None, output_tokens: int | None) -> None:
        self.calls += 1
        if input_tokens is None or output_tokens is None:
            self.calls_without_usage += 1
            return
        self.input_tokens += input_tokens
        self.output_tokens += output_tokens

    def status_fields(self) -> dict[str, object]:
        """The pieces that ride on the run's single terminal status line."""
        fields: dict[str, object] = {
            "calls": self.calls,
            "in_tokens": self.input_tokens,
            "out_tokens": self.output_tokens,
        }
        if self.calls_without_usage:
            # Say so rather than letting the totals read as complete.
            fields["calls_missing_usage"] = self.calls_without_usage
        return fields


class Backend(Protocol):
    name: str
    usage: Usage

    def complete(self, system: str, user: str) -> str:
        """Return the model's text response."""


class AnthropicBackend:
    """Claude via the official Anthropic SDK."""

    name = "anthropic"

    def __init__(self) -> None:
        from anthropic import Anthropic

        # Zero-arg client also picks up an `ant auth login` profile, so this
        # works in CI (API key) and on a workstation (OAuth profile) alike.
        self.client = Anthropic()
        self.model = os.environ.get("MODEL_NAME", "claude-opus-5")
        self.effort = os.environ.get("MODEL_EFFORT", "high")
        self.usage = Usage()

    def complete(self, system: str, user: str) -> str:
        # Streaming because doc pages routinely run past the non-streaming
        # HTTP timeout at this max_tokens.
        with self.client.beta.messages.stream(
            model=self.model,
            max_tokens=64000,
            system=system,
            messages=[{"role": "user", "content": user}],
            thinking={"type": "adaptive"},
            # MODEL_EFFORT is a deploy-time string, so it cannot satisfy the
            # SDK's Literal-typed TypedDict statically. Ignored rather than
            # cast, because importing the beta param type would couple this
            # file to an SDK path that has already moved once.
            output_config={"effort": self.effort},  # type: ignore[arg-type]
            # Route around a safety refusal instead of failing the whole run.
            betas=["server-side-fallback-2026-07-01"],
            fallbacks="default",
        ) as stream:
            message = stream.get_final_message()

        reported = getattr(message, "usage", None)
        self.usage.record(
            getattr(reported, "input_tokens", None),
            getattr(reported, "output_tokens", None),
        )

        if message.stop_reason == "refusal":
            detail = getattr(message, "stop_details", None)
            category = getattr(detail, "category", None)
            raise RuntimeError(f"model declined the request (category={category})")

        return "".join(b.text for b in message.content if b.type == "text")


class OpenAICompatBackend:
    """DigitalOcean GenAI agents, or any other OpenAI-shaped endpoint."""

    name = "openai-compat"

    def __init__(self) -> None:
        from openai import OpenAI

        base_url = os.environ.get("MODEL_BASE_URL")
        if not base_url:
            raise RuntimeError(
                "MODEL_BASE_URL is required for the openai-compat backend"
            )

        # Refuse an empty model here rather than trusting the deploy config or
        # the remote API to reject it. `model` is an OPTIONAL field for
        # OpenAI-compatible routers, so an empty value can resolve to an
        # account default: some other model runs and this harness attributes
        # the result to the candidate it meant to score. A wrong number that
        # looks right is the failure an evaluation can least afford.
        model = os.environ.get("MODEL_NAME", "").strip()
        if not model:
            raise RuntimeError(
                "MODEL_NAME is required for the openai-compat backend and must "
                "not be empty: an empty model lets the router choose one, and "
                "the result would be attributed to the wrong model"
            )
        self.model = model

        # What the router says it actually served. Requesting a model is not
        # proof the response came from it.
        self.resolved_model: str | None = None
        self.usage = Usage()

        self.client = OpenAI(
            base_url=base_url,
            api_key=os.environ.get("MODEL_API_KEY", ""),
        )

    def complete(self, system: str, user: str) -> str:
        response = self.client.chat.completions.create(
            model=self.model,
            max_tokens=int(os.environ.get("MODEL_MAX_TOKENS", "16000")),
            messages=[
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
        )

        # Optional in the OpenAI schema, and some routers omit it, so an
        # absent block is recorded as unavailable rather than as zero.
        reported = getattr(response, "usage", None)
        self.usage.record(
            getattr(reported, "prompt_tokens", None),
            getattr(reported, "completion_tokens", None),
        )

        served = getattr(response, "model", None)
        self.resolved_model = served
        if served and served != self.model:
            # Not fatal - a router may legitimately answer with a more specific
            # id than the one asked for (a dated or quantised variant). But a
            # score only means something against the model that produced it, so
            # record which one that was, in the log the run leaves behind.
            print(
                f"NOTE: requested model {self.model!r} but the router served "
                f"{served!r}; attribute results to the latter",
                flush=True,
            )

        return response.choices[0].message.content or ""


class ClaudeCliBackend:
    """Local Claude Code CLI, for validating prompts without an API key.

    Development only. The CLI is not in the runner image and carries a
    workstation's interactive credentials, so this backend cannot run in the
    container or in CI - it exists so the prompts can be exercised against a
    real model before anyone goes looking for an API key.
    """

    name = "claude-cli"

    # Nothing here needs tools; denying them keeps a prompt that happens to
    # read like an instruction from touching the filesystem.
    DENIED_TOOLS: ClassVar[list[str]] = [
        "Bash", "Edit", "Write", "NotebookEdit", "Read", "Glob", "Grep",
        "WebFetch", "WebSearch", "Task", "Agent",
    ]

    def __init__(self) -> None:
        self.binary = os.environ.get("CLAUDE_CLI", "claude")
        self.model = os.environ.get("MODEL_NAME", "claude-opus-5")
        self.timeout = int(os.environ.get("CLAUDE_CLI_TIMEOUT", "900"))
        # `--output-format text` returns prose and no accounting, so every call
        # here counts as usage-unavailable. Recorded rather than skipped so the
        # call count stays right and the totals are visibly incomplete.
        self.usage = Usage()

    def complete(self, system: str, user: str) -> str:
        import subprocess

        cmd = [
            self.binary,
            "--print",
            # No --bare here: it also skips keychain reads, which is where the
            # CLI's credentials live, so the call fails with "Not logged in".
            # --strict-mcp-config alone keeps MCP servers out of a text call.
            "--strict-mcp-config",
            "--model", self.model,
            "--system-prompt", system,
            "--output-format", "text",
            "--disallowed-tools", *self.DENIED_TOOLS,
        ]
        # The user prompt carries a whole diff plus a page body; stdin avoids
        # any argv length limit.
        result = subprocess.run(
            cmd, input=user, capture_output=True, text=True, timeout=self.timeout
        )
        self.usage.record(None, None)
        if result.returncode != 0:
            raise RuntimeError(
                f"claude CLI exited {result.returncode}: {result.stderr.strip()[:2000]}"
            )
        return result.stdout


def load() -> Backend:
    """Pick a backend from MODEL_BACKEND (default: anthropic)."""
    choice = os.environ.get("MODEL_BACKEND", "anthropic").strip().lower()
    if choice == "anthropic":
        return AnthropicBackend()
    if choice in ("openai-compat", "digitalocean", "do-genai"):
        return OpenAICompatBackend()
    if choice in ("claude-cli", "cli"):
        return ClaudeCliBackend()
    raise RuntimeError(f"unknown MODEL_BACKEND: {choice!r}")
