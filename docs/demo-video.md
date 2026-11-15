# Demo video: script and shot list

**Target:** under 3 minutes, a human voice, and the wow moment in the first 30 seconds. Every shot
is the real console against the real demo stack, with no mock-ups.

**Already built:** a narrated video (about 3:50, a launch intro followed by this walk-through,
with captions) is generated from the running demo by `console/scripts/record.mjs` and
`brag-output/build.mjs`. See [brag-output/README.md](../brag-output/README.md). The script below is
for recording a version with a human voice.

## Before recording

1. Run `./demo.sh` and open <http://localhost:18788/> in a clean browser window at 1440×900 (or
   record at 2560×1440 and scale down). Sign in.
2. Close the first-run tour. In the demo panel, turn **Normal work** on and let it run for a
   minute, so the airspace is busy and the timeline has history.
3. Hide the bookmarks bar and notifications. Zoom the browser to 100%.
4. Recorder: OBS (free) at 60 fps. Record the voice separately if you can, in a quiet room,
   then lay it over the video.
5. Rehearse each shot once. The attacks are deterministic, so each one behaves the same every
   time.

Reset between takes with `./demo.sh stop` and then `./demo.sh`.

## Script

Times are cumulative. **Say** is the voice-over. **Do** is what happens on screen.

### 0:00–0:25 · The hook: an attack, stopped in mid-air

**Do:** Open on the airspace with normal work flowing (cyan flights). Click **Refund borrowed
through another agent**. The last leg turns red and stops mid-air, with a red flare and the
**✕ refunds-need-an-operator** marker. The toast says "Stopped".

**Say:** "This is every call between our AI agents, live. A customer just hid an instruction
in a chat message. The support agent can't issue refunds, so it asked the billing agent to send
fifty thousand rupees to the attacker. The gateway allowed every single hop. Gjallarhorn saw the
whole chain, and stopped the money before it moved."

### 0:25–0:50 · The problem

**Do:** Hover over the chain pills in the interceptions card (customer-chat › support ›
billing → billing.refund). Then cut to a still frame with three lines of text:
- "Which agent did this?"
- "Who asked whom?"
- "Was a permission borrowed?"

**Say:** "Teams now run many agents that call models, use tools and ask each other for help.
When one is tricked, today's gateways can't say which agent did it, who asked whom, or whether a
permission was borrowed. OWASP calls this the confused deputy. Gjallarhorn is a plugin for the
Bifrost gateway that answers all three, on every call."

### 0:50–1:25 · Seeing the whole chain, and locking an agent

**Do:** Click the red ✕ marker. The incident panel opens: the chain highlights in the airspace
while everything else dims; steps are listed with decisions; the AI co-pilot explains in plain
words. Click **Lock billing** and give a reason. The billing node goes red with a shield; the
next flights toward billing are refused ("✕ lockdown").

**Say:** "One click shows the whole chain: who started it, every agent it passed through, and
the rule that stopped it, explained in plain words by the AI co-pilot. If an agent misbehaves,
one button cuts it off from models, tools and other agents, on its very next call, without
locking out the people it works for. We measured it: about two milliseconds."

**Do:** Unlock billing.

### 1:25–2:20 · The AI co-pilot closes a gap, and refuses its own bad ideas

**Do:** Open **Policy** (key 3). Delete the email rule. The live replay check says "Safe". Save.
Back to the airspace, click **Forum post makes an agent email customer data out**. The toast
says "Harm caused: email-send ×1".

**Say:** "No policy is perfect. Here I remove one rule, and an attack gets through: a forum post
tricks an agent into emailing customer data out."

**Do:** Open **Table** (key 2), type `email attack`, open the call. Click **Suggest a rule**.
Scroll through the cards: three **Proven safe**, then the red **Rejected** ones ("would block 74
pieces of normal work", plus one with an unknown field). Apply the top proven rule. Back to the
airspace, run the same attack: it is cut off by the new rule.

**Say:** "I ask the co-pilot for a fix. It suggests nine rules, and it doesn't trust any of
them. Each one is replayed against all the real traffic we recorded. Three are proven safe. Six
are rejected on their own: five would have blocked normal work, one was malformed. I apply the
proven one, proven again on the server. Same attack: blocked."

### 2:20–2:45 · Proof

**Do:** Show the README's claims table, or a slide with:
- "31 attacks: 12 harmful without Gjallarhorn, 0 with it"
- "11/11 normal work, 0 false blocks"
- "lockdown in about 2 ms"
- "every number links to a raw result file"

**Say:** "We built an attack lab with thirty-one attacks through the real gateway. Without
Gjallarhorn, twelve of them caused real damage. With it, none did, and nothing normal was
blocked. Every number links to the raw result file, and the threat model says plainly what it
doesn't protect against."

### 2:45–3:00 · Close

**Do:** A terminal running `./demo.sh`, then the console's login screen, then the logo.

**Say:** "It's open source. It runs on a standard Bifrost through the official plugin hooks, and
one command starts the whole demo. Gjallarhorn: the horn that warns you when your agents go
wrong."

## Checklist after recording

- [ ] Under 3:00.
- [ ] The wow moment (red cut-off) happens before 0:25.
- [ ] Captions for everything said (many judges watch muted).
- [ ] No real tokens or personal data on screen (the demo token is public and fine).
- [ ] The scripted co-pilot model is not presented as a real LLM: its explanations say so on
      screen, which is enough.
