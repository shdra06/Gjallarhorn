# AI co-pilot

When something goes wrong, an operator needs to know two things fast: what happened, and what
rule would stop it next time without breaking anything else. The co-pilot answers both. It does
not trust its own answers, though. A model can sound sure of itself and still be wrong, so every
rule it suggests is replayed against real recorded traffic before anyone can switch it on.

Code: `engine/incident` (rebuilding an incident), `engine/copilot` (explainer and rule suggester),
`engine/control/copilot.go` (API).

## 1. Rebuilding the incident

`incident.Build(events, seq)` starts from any recorded call and rebuilds the whole chain around it
from the flight recorder. It walks up through the chain tickets to the call that started it all,
then back down to every call made inside that chain. The result has:
- the origin (who started it) and every agent involved;
- each step, with its plane, target, decision and depth in the tree;
- whether each step actually ran, which includes calls only flagged in monitor-only mode;
- any lockdowns of those principals during the incident.

Unrelated traffic before and after is left out. Every fact comes from the recorder, never from a
model.

## 2. Explaining it

`copilot.Explain` always builds a **grounded** explanation first, written straight from the
facts:
- a title;
- a numbered story of each step;
- why the call was blocked or allowed, quoting the rule that decided;
- a risk line;
- concrete next steps, such as "Lock agent:research while you investigate."

If a model is configured, it gets the same facts as JSON and adds a short narrative in plain
English. The system prompt tells it to use only those facts and to treat every name and reason
as data, never as instructions. That matters because agent names and tool targets can come from
attackers. The reply has control characters stripped and is capped at 2,000 characters. If the
model fails or answers nonsense, the grounded explanation still comes back, with the model error
attached.

## 3. Suggesting rules, and proving them

`copilot.Suggest` collects candidate rules from two places:
- **Synthesised from the incident**, from narrow to broad. For example: "this agent, this tool,
  in chains started by an outside party", or "this tool via this agent", up to "refunds only for
  operators" or "the whole tool family". Narrow rules are safer. Broad rules are offered too, so
  the replay can show why they're a bad idea.
- **From the model**, which is asked for at most three rules as JSON. Parsing is strict:
  - a rule with a field Gjallarhorn doesn't know is rejected, never quietly ignored;
  - IDs are forced to start with `copilot-`.

Then each candidate goes through the same proof:
1. **Validate** it with the policy validator.
2. **Rebase** all recorded history under the *current* policy, so a candidate is judged only on
   what it adds. This also covers traffic recorded in monitor-only mode.
3. **Replay** that history with the candidate added.
4. **Judge**:

| Result | Meaning |
|---|---|
| `rejected` | It doesn't stop the call, or it would block **normal work** (calls labelled benign). It never comes with an applicable policy |
| `review` | It stops the call, but also blocks other unlabelled calls. A human should look at those first |
| `proven` | It stops the call and blocks nothing else in the recorded traffic. "Also catches N other attacks" is a bonus |

Suggestions are ranked with proven rules first. Among those, the ones that also catch other
attacks come next, then simpler rules.

## 4. Applying a rule

`POST /v1/copilot/apply` doesn't take the earlier proof on trust. It:
- needs the `policy_etag` from the suggestion as `If-Match`, so a stale suggestion can't
  overwrite a newer policy;
- **proves the rule again**, against the current policy and everything recorded up to that
  moment, so traffic that arrived after the suggestion counts too;
- refuses `rejected` rules with `409 Conflict`, even when a client sends them on purpose;
- refuses `review` rules unless `accept_review` is set;
- installs the rule with a compare-and-swap. If someone else changed the policy while the proof
  ran, the request fails with `412` instead of overwriting their change.

The change is written to the flight recorder as "<operator> via co-pilot".

## API

All endpoints need the admin token.

| Endpoint | What it does |
|---|---|
| `GET /v1/incidents?limit=50` | Recent incidents: blocked or flagged calls, newest first, one per chain |
| `GET /v1/incidents/{seq}` | The full incident around any recorded call |
| `GET /v1/copilot` | Which model the co-pilot uses (`mode: llm`), or `mode: grounded` |
| `POST /v1/copilot/explain` `{"seq": N}` | Explanation of the incident around call N |
| `POST /v1/copilot/suggest` `{"seq": N}` | Proven, review and rejected rules, plus `policy_etag` |
| `POST /v1/copilot/apply` `{"seq": N, "rule": {...}}` + `If-Match` | Prove again, then apply |

Explain and suggest can be slow with a real model, so at most two run at once. A third gets
`429`.

## Configuration

```json
"copilot": {
  "endpoint": "http://127.0.0.1:8080",
  "model": "openai/gpt-4o-mini",
  "virtual_key": "env:GJ_COPILOT_VK",
  "timeout_seconds": 45
}
```

- **Point it at the Bifrost the plugin runs in, with a virtual key of its own.** Then the
  co-pilot's model calls go through Bifrost like everyone else's: budgets, logs, and
  Gjallarhorn's own policy all apply. Give that key a principal (the demo uses
  `service:copilot`).
- Any OpenAI-compatible endpoint works. Use `api_key` (sent as a bearer token) for endpoints that
  aren't Bifrost.
- Leave `copilot` out to run without a model. Explanations are then grounded only, and rules are
  synthesised only. The proof is the same either way.

## The demo's stand-in model

The demo runs offline, so its "model" is a script (`demo/internal/stubmodel`, model
`mock/copilot-stub`), not a real LLM. Its explanations say so. When asked for rules it always
proposes three on purpose:
- a precise rule;
- a dangerously broad one: block the whole tool family for everyone;
- a malformed one, with a field Gjallarhorn doesn't know.

That makes the fire drill repeatable. Gjallarhorn has to prove the first rule and throw out the
other two by itself, every time. To use a real model, add a provider to Bifrost and change
`model`. Nothing else changes.

## Fire drill

The attack lab ends with a co-pilot fire drill (`lab.RunDrill`, `attacklab -mode drill`):
1. Remove the email rule, which opens a gap.
2. Run normal work, then the data-exfiltration attack, which gets through and sends the data out.
3. Find the harmful call in the flight recorder and ask the co-pilot to explain it.
4. Ask for rules, and check that the unsafe ones are rejected because they'd block normal work.
5. Try to apply a rejected rule anyway. The API must refuse it.
6. Apply the best proven rule.
7. Run the attack again (it must be stopped by the new rule) and all normal work again (it must
   all get done, with no false blocks).
8. Restore the original policy.

Results are in [results.md](results.md).
