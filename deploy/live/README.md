# Running an always-on public demo

This puts the Gjallarhorn demo on the internet: the console over HTTPS, with demo traffic and
the attack buttons, reset to its starting state every hour.

## What you need

- **A Linux VM** with 2 vCPUs and 4 GB of RAM (the first build compiles Bifrost), any cloud.
  Both x86-64 and ARM work, because everything builds from source.
- **A domain name** (or a subdomain) pointing at the VM, with ports 80 and 443 open.
- **Docker** with the compose plugin.

## Steps

```bash
sudo git clone https://github.com/shdra06/Gjallarhorn.git /opt/gjallarhorn
cd /opt/gjallarhorn
cp deploy/live/env.example deploy/live/.env
# Edit deploy/live/.env: your domain, and two long random secrets:
#   openssl rand -hex 24   (admin token)
#   openssl rand -hex 32   (ticket key)
sudo docker compose -f deploy/live/compose.yaml --env-file deploy/live/.env up -d --build
```

The first start builds everything (10–15 minutes). Then open `https://<your domain>/` and click
**Start the demo**. The demo config has `control.public_demo` on, so visitors (the judges) need no
token: the console fetches it from `/v1/demo-access`. To require the token instead, remove
`"public_demo": true` from `deploy/demo/config.json` and share the admin token from `.env`.

Reset it every hour, so visitors' lockdowns and policy edits don't pile up:

```bash
(sudo crontab -l 2>/dev/null; echo "0 * * * * cd /opt/gjallarhorn && bash deploy/live/reset.sh >> /var/log/gjallarhorn-reset.log 2>&1") | sudo crontab -
```

## What visitors can and can't do

- **Anyone who opens the page gets full control** (through `public_demo`, or the shared admin
  token if you turned that off). With it, a visitor can launch attacks, lock agents, edit the policy and ask the
  co-pilot. That is the point of the demo. The hourly reset undoes it.
- **Nothing real is behind it.** The demo's "company" is simulated: the refunds, emails and shell
  commands are recorded in a ledger and never happen, the model is a mock, and no outside
  service is called.
- **Only the console is public.** Bifrost's own port stays inside the compose network.
- **Use fresh secrets.** Don't reuse the demo token or ticket key from this repository.

## Updating

```bash
cd /opt/gjallarhorn && sudo git pull
sudo docker compose -f deploy/live/compose.yaml --env-file deploy/live/.env up -d --build
```
