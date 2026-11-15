# Hyperframes composition brief: Gjallarhorn

## Objective
One video: a cinematic launch intro (about 22 s) that hands over to a narrated product demo.
Under 5 minutes in total.

## Output
- Composition directory: `brag-output/composition/`
- Rendered video: `brag-output/gjallarhorn-demo.mp4`, poster `brag-output/brag.jpg`
- Format: landscape, 1920×1080, 30 fps

## Source material
- Project root: `gjallarhorn/`
- Read: README.md, docs/console.md, docs/demo-video.md, docs/results.md,
  console/src/styles.css, console/src/components/Logo.tsx, console/src/three/Airspace.ts
- Product name: Gjallarhorn
- Tagline: "Badges, chain tracking, an agent lockdown button and a flight recorder for AI
  agents, built into the Bifrost AI gateway."
- Key UI moment: the red flight cut off mid-air with "✕ refunds-need-an-operator"
- Footage: real console recordings made by `console/scripts/record.mjs` against the running demo
  (`hook`, `copilot`, `tour`), each with a `.marks.json` of when every action happened
- Copy that must appear verbatim:
  - "✕ refunds-need-an-operator"
  - "customer-chat › support › billing → billing.refund"
  - "12 of 31 attacks cause real harm without Gjallarhorn, and 0 of 31 with it"
  - "./demo.sh" and "github.com/shdra06/Gjallarhorn"

## Creative direction
- Tone: cinematic intro, then a calm, clear walk-through
- Hook: the attack chain typing in, ending on ₹50,000 in red
- Outro: "Gjallarhorn. The horn that warns you when your agents go wrong."
- Avoid: generic SaaS language, abstract filler, redesigning the console

## Visual identity
- Background `#060a13` (second `#0a1120`), text `#e6edf7`, muted `#93a5bc`
- Accent cyan `#38e1ff`, blocked red `#ff4d6a`, amber `#ffb020`, green `#3ddc97`
- Display Oswald (uppercase), body Roboto, mono IBM Plex Mono
- The horn mark from Logo.tsx

## Storyboard
`brag-plan.md` is the contract: intro scenes 1–5, demo scenes 6–13.

## Audio
- Role: cinematic support in the intro, a quiet bed under the demo voice
- Music: `happy-beats-business-moves-vol-12-by-ende-dot-app.mp3`, 0.34 → ducked 0.12 under the
  demo voice, fade out at the end
- Cue guidance: bundled preset; name reveal locked to the strong cue at 8.74 s
- Voice: Kokoro `af_heart`, one file per line in `assets/vo/`, timed by the edit
- SFX: few and exact (cut-off impact, name bell, pointer clicks)
- Captions: every spoken line, sentence groups, bottom of frame, title-safe
