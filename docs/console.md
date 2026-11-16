# Operator console

The console is where an operator sees what Gjallarhorn sees, live. They can stop an attack, lock
an agent, ask the AI co-pilot what happened, and test a rule against real traffic before
switching it on.

Code: `console/` (React 19, Three.js, TypeScript, Vite). The control API serves it, so it lives on
the same origin as the API, with no extra server and no CORS.

## Run it

```bash
podman build -t gjallarhorn-bifrost -f deploy/Containerfile .
podman build -t gjallarhorn-demo -f deploy/demo/Containerfile .
bash deploy/demo/run-console.sh
```

Then open <http://localhost:18788/>. The landing page shows a live 3D airspace and what
Gjallarhorn does; click **Start the demo**. It signs in and turns on normal work for you.

**Where the token comes from.** The demo config sets `control.public_demo: true`, so
`GET /v1/demo-access` hands the admin token to the console and nobody has to type it. Without that
setting (the default) the endpoint returns `{"public_demo": false}`, and the landing page asks for
the token and points to the terminal, where `run-console.sh` prints it (`Admin token: …`).

**Light and dark.** The ☀ / ☾ / system buttons in the top bar switch the theme; dark is the
default. The layout is the same in both.

In the demo panel:
1. Turn on **normal work**.
2. Launch an attack, for example **Refund borrowed through another agent**.

Stop the demo with `bash deploy/demo/run-console.sh stop`.

**Known issue:** Bifrost downloads model pricing data from the internet when it starts. If that
download times out, Bifrost exits with `failed to initialize pricing manager`. This happened
once in our measurement runs. Run the script again, and check that the machine can reach the
internet.

To work on the console itself, keep the demo running and start the dev server. It proxies `/v1`
and `/demo` to the demo stack, so the browser still sees one origin:

```bash
cd console
npm ci
npm run dev        # http://localhost:5173
npm test           # unit and component tests
npm run build      # type-check, then build to console/dist
```

The README screenshots come from a running demo, taken with headless Chrome or Edge:
`node console/scripts/screenshots.mjs` (saves to `docs/images/`). The demo video's footage is
recorded the same way, with a drawn pointer, by `node console/scripts/record.mjs` (see
[brag-output/README.md](../brag-output/README.md)).

## What's on screen

### Airspace (key 1)

A live 3D map of every call crossing the gateway.

| You see | It means |
|---|---|
| Four columns, left to right | People and apps → agents → tools → models. Work flows the way a chain does |
| A cyan flight | A call that was allowed |
| A red flight that stops mid-air, a red flare and a **✕ rule** marker | Gjallarhorn blocked it. The marker is a button that opens the incident |
| An amber flight with a **⚠** marker | A denied call that ran anyway, because Gjallarhorn is in monitor-only mode |
| An agent flashing red | Its call was just refused |
| A red wireframe shield and a **🔒 LOCKED** label | The agent is locked |
| An orange ring | An outside party, such as the customer chat (`trust=external`) |
| Faint lines | The "air corridors" recorded traffic has used. Busier routes glow more, and recently blocked ones are red |

Around the map:
- **Last minute:** calls per minute, blocked, flagged, locked agents, and the mode.
- **Interceptions:** the latest blocked or flagged calls. Each one opens its incident.
- **Demo traffic** (demo only): a loop of normal work, and one button per attack. Each attack
  runs through the real gateway. The result comes from the effects ledger, for example "Harm
  caused: billing-refund ×1" or "Stopped".
- **Replay timeline:** a histogram of recorded calls, with blocked calls in red.
  - Drag back in time and press play to replay the airspace at 1×–64×.
  - **Replay last minute** is one click.
  - **Live** returns to live traffic.

Selecting an incident highlights its whole chain and dims everything else.

### Incident panel

Open it from a marker, the interceptions list, a table row or a node's recent calls. It shows:
- **The chain** as pills, and every step in it, nested by depth, with its decision.
- **The AI co-pilot's explanation:**
  - why the call was blocked or allowed, quoting the rule;
  - how risky it was;
  - what to do next;
  - a narrative from the model when one is configured. The demo's stand-in model labels itself
    as scripted.
- **Suggest a rule**, for calls that actually ran. Every candidate comes back **proven**, **needs
  review** or **rejected**, with:
  - the rule written out in words ("Deny when target email.send · started by label
    trust=external");
  - the replay verdict, and how much normal work it would block;
  - the other calls it would block.

  Rejected rules have no apply button. **Apply rule** asks for confirmation. The server then
  proves the rule again against everything recorded and applies it only if the policy hasn't
  changed in the meantime.
- **Lock** the agent that made the call.

### Node panel

Click a node or its label to see:
- its kind and labels;
- its traffic in the loaded history;
- its recent calls;
- the **Lock** button. It asks for a reason, which is recorded in the flight recorder, and for a
  duration (until lifted, 15 minutes, 1 hour or 24 hours).

If an agent is locked, the panel says who locked it and when, and offers to unlock it. That
includes automatic lockdowns from a rule.

### Table (key 2)

Every recorded call, newest first:
- Filter by blocked, flagged or allowed, by kind (agent, tool or model call), and by text. Every
  word you type must match, so `email attack` finds attack calls to email tools.
- Bifrost's own tool-discovery calls, which it runs every 10 minutes, are hidden unless you ask.
- Rows open with a click, Enter or Space.

### Policy (key 3)

A JSON editor for the live policy, with a rule list on the left. As you type, the console:
1. checks the JSON;
2. validates the policy on the server;
3. **replays every recorded call against it**, then shows:
   - whether it's safe, meaning it still blocks every attack it blocked before and no more
     normal work;
   - how many calls it newly blocks or allows, by label;
   - each changed call, linked to its incident.

**Save** sends `If-Match`, so you can't overwrite someone else's change. Saving a policy that
fails the replay check needs an extra confirmation. The check can only judge traffic that has
been recorded, and the panel says so.

### Mode switch

**Enforcing** blocks denied calls. **Monitor-only** records them and lets them run, which is
what you have without Gjallarhorn. Switching to monitor-only asks for confirmation.

## Accessibility

- **Everything in 3D is also in words.** The table, the interceptions list and the incident
  panel carry all the same information. Without WebGL, the console offers the table instead.
- **Keyboard:**
  - <kbd>1</kbd> <kbd>2</kbd> <kbd>3</kbd> switch views;
  - <kbd>Tab</kbd> reaches every node label and incident marker in the 3D scene;
  - <kbd>Enter</kbd> opens them;
  - <kbd>Esc</kbd> closes panels and dialogs;
  - <kbd>?</kbd> opens the help;
  - a skip link jumps to the main content.
- **Screen readers:** a polite live region announces blocks, flags and lockdowns, a few seconds
  apart and summarised when there are many. Dialogs trap focus and return it when they close.
- **Reduced motion:** the console follows `prefers-reduced-motion`, and the ◆ button in the top
  bar overrides it. In reduced-motion mode:
  - flights appear as still arcs;
  - there are no travelling dots, bursts, camera drift or radar sweep;
  - CSS transitions are switched off.
- **Never colour alone:** every status also has an icon and a word (✓ Allowed, ✕ Blocked,
  ⚠ Flagged, 🔒 Locked), and node kinds have different shapes. Text meets WCAG AA contrast on the
  dark background.
- **Focus** is always visible as a yellow ring.

## Security

- **The admin token:**
  - is typed in once and kept in `sessionStorage`, so it's gone when the tab closes;
  - is sent only in the `Authorization` header;
  - never appears in a URL. The live stream uses `fetch` with that header rather than
    `EventSource`.
- **Console responses** carry a strict Content-Security-Policy (`default-src 'self'`, no inline
  scripts, `frame-ancestors 'none'`), plus `nosniff`, `no-referrer` and `X-Frame-Options: DENY`.
- **Static files** are public, but everything they do goes through the token-protected API.
  Unknown `/v1/` paths return JSON 404s rather than the console.
- **The demo driver** is reached through `/demo/` on the control API. That route needs the admin
  token, which is stripped before the request is forwarded. Set `control.demo_driver` only in
  demo environments.
- **Text from the model or from agents** is rendered as text, never as HTML.

## Configuration

In the plugin config:

```json
"control": {
  "listen": "0.0.0.0:8787",
  "admin_token": "env:GJ_ADMIN_TOKEN",
  "console_dir": "/app/console",
  "demo_driver": "http://127.0.0.1:9300"
}
```

- `console_dir`: the built console (`console/dist`). Leave it out to serve only the API.
- `demo_driver`: demo only. It's the attack lab's `-serve` mode, which runs scenarios and a
  normal-work loop for the demo panel.

## Usability testing

The console has to make sense to someone who has never seen it within 30 seconds. The test
script is in [usability-test.md](usability-test.md).
