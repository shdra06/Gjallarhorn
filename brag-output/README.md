# The Gjallarhorn video

One video, about 3 minutes 50 seconds: a short launch intro that runs straight into a narrated
demo of the real console. Every shot of the console is a recording of the running demo, and every
number on screen comes from the README's results table.

| File | What it is |
|---|---|
| `gjallarhorn-demo.mp4` | The rendered video (not in git; rebuild it as below) |
| `brag.jpg` | The poster frame, also baked in as the video's first frame |
| `share-copy.txt` | A one-paragraph caption for posting |
| `brag-plan.md`, `composition-brief.md` | The plan, storyboard and look |
| `voice/lines.json` | Every narrated line. `say` is a phonetic spelling for the voice; captions use `text` |
| `build.mjs` | Writes the whole edit from the narration's lengths and the footage's marks |
| `composition/` | The HyperFrames project the build writes |

## Rebuild it

You need Node 22+, ffmpeg, a running demo (`./demo.sh`) and Chrome or Edge.

```bash
# 1. The narration (Kokoro, local): one file per line, plus their lengths
node brag-output/voice/tts.mjs

# 2. The console footage, acted in time with the narration
node console/scripts/record.mjs --out brag-output/composition/assets/footage --voice brag-output/voice --warm 75

# 3. The edit, the checks and the render
node brag-output/build.mjs
cd brag-output/composition
npx hyperframes check
npx hyperframes render --quality delivery --output ../gjallarhorn-demo.mp4
```

Record on a freshly started demo (`./demo.sh stop`, then `./demo.sh`) so the interceptions list
starts empty. The recorder puts the policy back the way it found it.

## Credits

- Music: "Happy Beats / Business Moves Vol. 12" by Sascha Ende (ende.app), CC BY 4.0
- Sound effects: Kenney.nl and unicae_games (OpenGameArt), CC0
- Voice: Kokoro-82M (`af_heart`), generated locally
- Built with HyperFrames
