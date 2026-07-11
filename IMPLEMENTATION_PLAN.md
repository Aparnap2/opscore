# IMPLEMENTATION_PLAN.md
_Last updated: 2026-07-09 by coding-agent_

## Goal
Support OpenRouter reasoning tokens (reasoning_details) for tencent/hy3:free and other reasoning-capable models.

## Status
- Total tasks: 6
- Completed: 0
- Remaining: 6

## Tasks

### Task 1: Update provider interface — 🔄 IN PROGRESS
- **Scope:** `internal/providers/interfaces.go`
- **Changes:** Add `ReasoningDetails *json.RawMessage` to `ChatMessage`. Change `Chat` signature to return `(string, *json.RawMessage, error)`.
- **Acceptance criteria:** Compiles. All implementations updated.
- **Test phase:** Build verification
- **Worktree:** main
- **Branch:** main (direct commit)

### Task 2: Fix fallback adapter — ⏳ TODO
- **Scope:** `internal/adapters/fallback/llm.go`
- **Changes:** Update `Chat` to new signature
- **Depends on:** Task 1

### Task 3: Fix Sarvam adapter — ⏳ TODO
- **Scope:** `internal/adapters/sarvam/llm.go`
- **Changes:** Update `LLMAdapter.Chat` and `LLMAdapterMock.Chat` to new signature
- **Depends on:** Task 1

### Task 4: Fix test stubs — ⏳ TODO
- **Scope:** `tests/agentic/stubs.go`, `internal/agents/agent_test.go`
- **Changes:** Update `StubLLM.Chat` and `mockLLM.Chat` to new signature
- **Depends on:** Task 1

### Task 5: Update OpenRouter adapter with reasoning — ⏳ TODO
- **Scope:** `internal/adapters/openrouter/llm.go`
- **Changes:** Add `ReasoningEnabled` to config, `extra_body` to request, parse `reasoning_details` from response, update `Chat` to return reasoning details
- **Depends on:** Task 1

### Task 6: Add live contract test — ⏳ TODO
- **Scope:** Create `tests/live/llm/openrouter_test.go`
- **Changes:** Raw HTTP test of OpenRouter reasoning API
- **Depends on:** Task 5

### Task 7: Build and verify — ⏳ TODO
- **Scope:** Run `go build ./...`, `go vet ./...`, domain tests, adapter tests
- **Depends on:** Tasks 2-6

## Known Risks / Gotchas
- All `LLMProvider` implementations must be updated at once or compilation will break
- Sarvam and fallback adapters return `nil` for reasoning details since they don't support it
- The `go build ./...` on the entire module is the definitive compile check
