# Console usability test

Phase 8 is done when 3–5 people who have never seen the console reach the "wow moment" within
30 seconds: they watch an attack get stopped and understand why. This is the script for that
test. Run it with real people. A heuristic review by the people who built it doesn't count.

**Status:** not run yet. Fill in the table at the bottom after each session.

## Setup (5 minutes, before each session)

1. Start the demo with `bash deploy/demo/run-console.sh`.
2. Open <http://localhost:18788/>, sign in, and clear the "toured" flag so the first-run tour
   shows: open the browser console and run `localStorage.removeItem('gj.toured')`.
3. Reload. Don't turn on traffic. The participant starts from the tour.
4. Start a timer when the airspace appears.

## Say this, and nothing more

> "This is a tool for teams that run AI agents. Please think aloud while you use it. There are
> no wrong answers. I'm testing the tool, not you."

Don't explain the screen or point at things. If they're stuck for 60 seconds, say: "What would
you try next?"

## Tasks

| # | Task (read aloud) | Success looks like |
|---|---|---|
| 1 | "Make something happen." | Turns on normal work or launches an attack within 30 seconds of the airspace appearing. **Note the time of the first attack.** |
| 2 | "Something was stopped. What was it, and why?" | Opens the incident (marker, feed or table) and says in their own words that a refund came from an outside customer message through support, so it was blocked. |
| 3 | "Make sure the billing agent can't do anything else for now." | Finds and uses **Lock**, gives a reason, and sees the agent locked. |
| 4 | "Put things back to normal." | Unlocks billing. |
| 5 | "Could a new rule have stopped the email attack? Find out without breaking anything." | Uses the policy view or **Suggest a rule** and explains the proven or rejected verdict. |
| 6 | "Show me the same thing without the 3D view." | Opens the table (button or <kbd>2</kbd>) and finds the blocked calls. |

## Questions afterwards

1. In one sentence, what does this tool do?
2. What was the most confusing moment?
3. What does a red line mean? An amber one? The orange ring?
4. Would you trust it to block traffic in your company? Why or why not?
5. On a scale of 1–5, how easy was it?

## Record

| Participant (no names) | Role | Time to first attack | Task 2 explained correctly? | Tasks done (of 6) | Ease 1–5 | Most confusing moment |
|---|---|---|---|---|---|---|
| P1 | | | | | | |
| P2 | | | | | | |
| P3 | | | | | | |
| P4 | | | | | | |
| P5 | | | | | | |

**Pass:** at least 3 of the participants launch an attack within 30 seconds, and all of them
explain task 2 correctly. Fix anything two or more people stumble over, then test again with new
people.
