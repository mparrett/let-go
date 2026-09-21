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
from pathlib import Path
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


class AttractorBackend:
    """Strange Lettractor's agentic pipeline, standing in for a single call.

    The point of this backend is the comparison, not the capability. Every other
    backend answers a prompt with one model call; this one hands the same system
    and user prompt to a DOT workflow that drafts, critiques its own draft
    against the task's criteria, and revises before answering. The harness
    around it is unchanged -- same page mapping, same validator, same pull
    request -- so a run through this backend and a run through `anthropic` on
    the same commit differ in exactly one thing, which is what makes the two
    numbers worth putting next to each other.

    The prompts travel as files rather than as DOT attributes. Attractor's maker
    stages read the working directory with their own file tools, and a page body
    plus a commit diff is far past what belongs in a graph attribute -- the
    task-runner example passes its request the same way, for the same reason.
    """

    name = "attractor"

    # Where the pipeline looks for its inputs and leaves its answer. Mirrors the
    # layout examples/task-runner/ uses, so the graph here reads like the ones
    # upstream ships rather than inventing a second convention.
    IN_SYSTEM = "task.system.md"
    IN_USER = "task.user.md"
    OUT_ANSWER = "state/answer.md"

    def __init__(self) -> None:
        self.binary = os.environ.get("ATTRACTOR_BIN", "attractor")
        self.graph = os.environ.get(
            "ATTRACTOR_GRAPH", "/app/pipelines/page-update.dot"
        )

        # Attractor addresses a model as `provider/name`, while MODEL_NAME here
        # is the bare id the Anthropic SDK takes. Deriving the qualified form
        # rather than asking for it twice keeps a comparison run honest: set
        # MODEL_NAME once and both backends use the same model. ATTRACTOR_MODEL
        # overrides for the case where they should deliberately differ.
        model = os.environ.get("ATTRACTOR_MODEL", "").strip()
        if not model:
            bare = os.environ.get("MODEL_NAME", "claude-opus-5").strip()
            provider = os.environ.get("ATTRACTOR_PROVIDER", "anthropic").strip()
            model = bare if "/" in bare else f"{provider}/{bare}"
        self.model = model

        # A whole pipeline, not one call: the default is the sum of several
        # model stages plus their shell gates. The upstream graph caps a model
        # stage at 20m on its own, so anything much below this truncates a run
        # that was still making progress.
        self.timeout = int(os.environ.get("ATTRACTOR_TIMEOUT", "1800"))

        # One record per complete(), deliberately, even though each one spends
        # several model calls internally. `calls` then means the same thing it
        # means for every other backend -- pages processed -- and the tokens are
        # reported as unavailable rather than as an undercount that would read
        # as a measurement. A cost comparison against the baseline has to come
        # from the provider's own billing, and saying so here is cheaper than
        # someone later trusting a number this cannot know.
        self.usage = Usage()

    def complete(self, system: str, user: str) -> str:
        import shutil
        import subprocess
        import tempfile

        # A fresh directory per page. Attractor's setup gate refuses to start
        # when its state directory already exists -- a deliberate guard against
        # two runs sharing one worktree -- and a docs run calls this once per
        # page, so reusing a directory would fail every call after the first.
        workdir = tempfile.mkdtemp(prefix="attractor-page-")
        try:
            base = Path(workdir)
            (base / "state").mkdir()
            (base / self.IN_SYSTEM).write_text(system, encoding="utf-8")
            (base / self.IN_USER).write_text(user, encoding="utf-8")

            cmd = [
                self.binary, "run", self.graph,
                # Unattended. Without it the pipeline's human gates block on
                # stdin forever inside a container nobody is watching.
                "--auto-approve",
                # `run` uses a mock model unless told otherwise, and a mock that
                # silently produces plausible text is the worst possible failure
                # for an evaluation.
                "--llm",
                "--model", self.model,
            ]
            result = subprocess.run(
                cmd, cwd=workdir, capture_output=True, text=True,
                timeout=self.timeout, check=False,
            )
            self.usage.record(None, None)

            answer = base / self.OUT_ANSWER
            if result.returncode != 0:
                # The pipeline's own log is the only account of which stage
                # failed, and it is on stdout. Tail rather than dump: a run that
                # went twenty minutes produces far more than a status line
                # should carry, and the container log has the whole thing.
                tail = (result.stdout or result.stderr).strip()[-2000:]
                raise RuntimeError(
                    f"attractor exited {result.returncode}: {tail}"
                )
            if not answer.exists():
                raise RuntimeError(
                    f"attractor exited 0 without writing {self.OUT_ANSWER}; "
                    "the graph reached its exit node without an answer stage, "
                    "which is a graph bug rather than a model refusal"
                )
            return answer.read_text(encoding="utf-8")
        finally:
            # The scratch holds a copy of the prompt, which holds the page and
            # the diff. Nothing downstream reads it, and a container that
            # processes several pages would otherwise accumulate all of them.
            shutil.rmtree(workdir, ignore_errors=True)


def load() -> Backend:
    """Pick a backend from MODEL_BACKEND (default: anthropic)."""
    choice = os.environ.get("MODEL_BACKEND", "anthropic").strip().lower()
    if choice == "anthropic":
        return AnthropicBackend()
    if choice in ("openai-compat", "digitalocean", "do-genai"):
        return OpenAICompatBackend()
    if choice in ("claude-cli", "cli"):
        return ClaudeCliBackend()
    if choice == "attractor":
        return AttractorBackend()
    raise RuntimeError(f"unknown MODEL_BACKEND: {choice!r}")
