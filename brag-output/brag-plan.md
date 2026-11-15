# Gjallarhorn: launch intro + narrated demo (one video)

The team asked for one video: a short /brag-style intro that runs straight into the narrated
product demo, under 5 minutes in total.

## Planning rubric

1. **What is it?** A Go plugin for the Bifrost AI gateway that gives every agent call a badge, a
   signed chain of who asked whom, a policy check, a lockdown button and a hash-chained flight
   recorder, with a live 3D console and an AI co-pilot that proves its own rules.
2. **Who is it for?** Teams running several AI agents behind a gateway, and the hackathon judges
   (Bifrost track, PS04).
3. **Strongest claim (grounded):** in a real Bifrost, 12 of 31 attacks cause real harm without
   Gjallarhorn and 0 of 31 with it; 11/11 normal work, zero false blocks (README, results/).
4. **Key visual:** the airspace: a red flight that stops mid-air with a red flare and the marker
   "✕ refunds-need-an-operator" (console/src/three/Airspace.ts).
5. **Product's own words:** "Badges, chain tracking, an agent lockdown button and a flight
   recorder for AI agents, built into the Bifrost AI gateway." (README line 3)
6. **The surprise:** the AI co-pilot rejects most of its own suggestions after replaying real
   traffic.
7. **Numbers on screen:** 31 attacks, 12 vs 0, 11/11 normal work, 1.8 ms median lockdown, about
   50 µs decision (45–56 µs median), 95.3% coverage. All from README's results table.
8. **Tone:** cinematic for the intro (dark, red/cyan, big type, beat-locked), then calm and clear
   for the demo (a person walking you through their console).
9. **Format:** landscape 1920×1080, 30 fps, Kokoro voice `af_heart`, captions on everything said.

## Angle

The gateway allowed every hop; the harm was in the chain. The intro tells that in four lines
over real footage, lands the name on a strong beat, then hands over to "Let me show you how."
The demo follows the script in docs/demo-video.md: the attack, the incident and a lockdown,
the co-pilot closing a gap and refusing its own bad ideas, the rest of the console, how it
works, the proof, and one command to try it.

## Look

- Ground `#060a13`, second ground `#0a1120`, panel `#0c1424`, hairline `#1c2a40`
- Ink `#e6edf7`, muted `#93a5bc`
- Allowed / accent cyan `#38e1ff`, blocked red `#ff4d6a`, flagged amber `#ffb020`,
  good green `#3ddc97`
- The horn logo from console/src/components/Logo.tsx
- Display: Oswald 600–700, uppercase, wide tracking (titles, eyebrows). Body: Roboto 400/700
  (the console's own fallback face). Mono: IBM Plex Mono (chains, rule ids, commands).
- Banned: generic SaaS lines, stock "AI" imagery, floating/pulsing titles, full-frame linear
  gradients (they band), equaliser graphics.

## Storyboard

Times are approximate; the voice is the clock and the edit follows the generated narration.

### Part 1: intro (about 22 s, music forward)

| # | Time | Picture | Voice |
|---|---|---|---|
| 1 Hook | 0–5 s | Dark ground. The chain types in as mono pills: `customer-chat › support › billing → billing.refund`, then `₹50,000` in red | i1, i2 |
| 2 Reveal | 5–9 s | Real footage: the red flight stops mid-air, marker "✕ refunds-need-an-operator". Punch-in on the marker | i3 |
| 3 Name | ~8.7 s | GJALLARHORN lands on the strong beat (8.74 s) with the horn mark; the README line underneath | (music) |
| 4 Highlights | 10–19 s | Three quick footage cards: billing locked (shield), co-pilot cards "Proven safe / Rejected", the 12 → 0 counter | i4, i5, i6 |
| 5 Hand-off | 19–22 s | The console's top bar slides up and becomes the demo's first frame | i7 |

### Part 2: demo (about 3.5 min, music ducked to 0.12)

| # | Picture | Voice |
|---|---|---|
| 6 Problem | Three questions as cards: "Which agent did this?", "Who asked whom?", "Was a permission borrowed?"; OWASP ASI03 tag | p1–p4 |
| 7 Attack | Footage `hook`: live airspace, pointer launches "Refund borrowed through another agent", red cut-off | a1–a3 |
| 8 Incident + lock | Footage `hook`: incident panel, co-pilot explanation, Lock billing, refused flights | n1–n4 |
| 9 Co-pilot | Footage `copilot`: rule removed, attack does harm, table search, suggestions proven / rejected, apply, attack blocked | c1–c9 |
| 10 Console tour | Footage `tour`: replay, node panel, table, policy | t1, t2 |
| 11 How it works | Diagram built from README's: caller → Bifrost hooks → engine (identity, ticket, policy, lockdown) → recorder → console / replay / co-pilot | h1–h3 |
| 12 Proof | Numbers card: 12/31 vs 0/31, 11/11, 1.8 ms, every number links to a raw file | r1–r4 |
| 13 Close | `./demo.sh`, github.com/shdra06/Gjallarhorn, the horn mark, "Geek Room Global AI Hackathon 2026 · Bifrost track" | e1, e2 |

## Voiceover script

The full text, line by line, is in `voice/lines.json` (generated with Kokoro `af_heart`):

> A customer hid one sentence in a chat message. Three agents later, fifty thousand rupees were
> about to leave. Gjallarhorn caught it mid-air. Lock a rogue agent in under two milliseconds.
> Let an AI co-pilot write the fix, and prove it first. Thirty-one attacks. Zero harm. Let me
> show you how.

Then the demo narration (p1–e2 in `voice/lines.json`).

Pronunciation: Kokoro's phonemizer reads "Gjallarhorn" as "Jee-jallarhorn" and "OWASP" as
"oh-asp", so those lines carry a phonetic `say` ("Yallarhorn", "Oh-wasp") for the voice while
the captions keep the real spelling. The co-pilot lines match what the recorded panel shows:
3 of 9 rules proven safe; of the 6 rejected, 5 would block normal work and 1 was malformed.

## Audio

- Music: `happy-beats-business-moves-vol-12` (steady, clean, 110 BPM), 0.34 in the intro,
  ducked to 0.12 under the demo narration, fading out under the close.
- Music cue guidance: bundled preset
  `assets/music/cues/happy-beats-business-moves-vol-12-by-ende-dot-app.music-cues.json`. Lock the
  name reveal to the strong cue at 8.74 s; the three highlight cards may sit on beats.
- SFX (sparse, polished): a soft impact when the flight is cut off, a bell on the name, light
  clicks where the pointer clicks in the footage, a riser-free outro.

## Grounding

Every number and claim on screen comes from README.md's results table or the console itself.
The co-pilot in the demo uses the scripted stand-in model, which labels itself on screen.
