// server_fuzz_test.go contains fuzz tests for pure translation functions.
package server_test

import (
	"encoding/json"
	"testing"

	"github.com/thelonelyghost/llm-proxy-provider-copilot/internal/server"
)

// FuzzResponsesToChat feeds arbitrary bytes into the Responses→chat-completions
// translation and enforces three invariants that hold regardless of input:
//
//  1. The function never panics.
//  2. When it succeeds (err == nil) the output is valid JSON.
//  3. When the input is itself valid JSON the function must not return an error
//     (any valid JSON shape should be translated, not rejected).
//
// Seed corpus entries cover the main branches: string input, array input,
// instructions, max_output_tokens, stream flag, and an empty object.
func FuzzResponsesToChat(f *testing.F) {
	seeds := []string{
		`{"model":"gpt-4o","input":"hello"}`,
		`{"model":"gpt-4o","input":"hello","stream":true}`,
		`{"model":"gpt-4o","input":"hello","instructions":"be helpful"}`,
		`{"model":"gpt-4o","input":[{"role":"user","content":"hi"}]}`,
		`{"model":"gpt-4o","input":[{"role":"user","content":"hi"}],"instructions":"sys"}`,
		`{"model":"gpt-4o","input":"hi","max_output_tokens":512}`,
		`{"model":"gpt-4o","input":"hi","temperature":0.7,"top_p":0.9}`,
		`{"model":"gpt-4o","input":"hi","tools":[{"type":"function"}],"tool_choice":"auto"}`,
		`{"model":"gpt-4o","input":"hi","reasoning":{"effort":"high"}}`,
		`{}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		out, _, err := server.ResponsesToChat(data)

		// Invariant 1: never panic (enforced automatically by the fuzzer harness).

		// Invariant 2: successful output must be valid JSON.
		if err == nil {
			if !json.Valid(out) {
				t.Errorf("responsesToChat returned invalid JSON on success; input=%q output=%q", data, out)
			}
		}

		// Invariant 3: valid JSON *object* input must not produce an error.
		// Non-object top-level JSON values (numbers, strings, arrays, null, bool)
		// are legitimately rejected because the Responses API always expects an
		// object; a 400 response for those is correct behaviour.
		var obj map[string]json.RawMessage
		if json.Unmarshal(data, &obj) == nil && err != nil {
			t.Errorf("responsesToChat returned error for valid JSON object input=%q: %v", data, err)
		}
	})
}
