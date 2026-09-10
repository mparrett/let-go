"""Model backends for the docs runner.

Two real clients behind one interface rather than a single OpenAI-compatible
path pointed at both: Anthropic's own SDK is the supported way to reach Claude,
and its compatibility shim gives up thinking, effort and refusal handling.
DigitalOcean's GenAI endpoints really are OpenAI-shaped, so that backend uses
the OpenAI SDK for what it actually is.
"""

from __future__ import annotations

import os
from typing import Protocol


class Backend(Protocol):
    name: str

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

    def complete(self, system: str, user: str) -> str:
        # Streaming because doc pages routinely run past the non-streaming
        # HTTP timeout at this max_tokens.
        with self.client.beta.messages.stream(
            model=self.model,
            max_tokens=64000,
            system=system,
            messages=[{"role": "user", "content": user}],
            thinking={"type": "adaptive"},
            output_config={"effort": self.effort},
            # Route around a safety refusal instead of failing the whole run.
            betas=["server-side-fallback-2026-07-01"],
            fallbacks="default",
        ) as stream:
            message = stream.get_final_message()

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
            raise RuntimeError("MODEL_BASE_URL is required for the openai-compat backend")

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
    DENIED_TOOLS = [
        "Bash", "Edit", "Write", "NotebookEdit", "Read", "Glob", "Grep",
        "WebFetch", "WebSearch", "Task", "Agent",
    ]

    def __init__(self) -> None:
        self.binary = os.environ.get("CLAUDE_CLI", "claude")
        self.model = os.environ.get("MODEL_NAME", "claude-opus-5")
        self.timeout = int(os.environ.get("CLAUDE_CLI_TIMEOUT", "900"))

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
