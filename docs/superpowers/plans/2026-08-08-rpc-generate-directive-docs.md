# RPC Generate Directive Documentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Explain the purpose, scope, and mechanics of the `//go:generate` RPC generator directive in the RPC basics guide.

**Architecture:** Update only the existing guide's contract example and nearby generation workflow text. Clarify that the directive is executed by `go generate`, uses the module-pinned generator through `go run`, scans the current package, and writes deterministic `.rpc.gen.go` output; distinguish this workflow convenience from runtime requirements.

**Tech Stack:** Markdown documentation, Go `go:generate` convention, `origingen rpc` command.

## Global Constraints

- Keep the change limited to `docs/baseline/v3.0/guides/06.rpc-basics.md` plus this plan record.
- Do not change generator behavior or generated files.
- Preserve the existing Origin version/path and Chinese documentation style.

---

### Task 1: Document the RPC generation directive

**Files:**
- Modify: `docs/baseline/v3.0/guides/06.rpc-basics.md:80-101`

**Interfaces:**
- Consumes: The existing `//go:generate go run github.com/duanhf2012/origin/v3/cmd/origingen rpc .` example.
- Produces: A self-contained explanation of directive execution, package scanning, generated output, version pinning, and runtime independence.

- [ ] **Step 1: Add explanatory prose immediately around the directive**

  Explain that `//go:generate` is run by `go generate`, `go run` resolves the generator from the current module's `go.mod`, `rpc .` scans the current package for `// origin:rpc`, and successful generation writes the corresponding `.rpc.gen.go` file.

- [ ] **Step 2: Clarify required versus optional usage**

  State that the directive is not required by RPC runtime startup or ordinary `go build`/`go test`, but is required by the documented `go generate` workflow and should remain in contract source files; manual generator invocation or an external script is the alternative.

- [ ] **Step 3: Verify the rendered Markdown structure**

  Run a targeted text inspection and confirm the directive, explanation, generated filename, and runtime distinction are all present without changing unrelated sections.

- [ ] **Step 4: Review the diff**

  Run `git diff -- docs/baseline/v3.0/guides/06.rpc-basics.md` and confirm only the requested documentation content changed.
