# Offline data for Bifrost

Bifrost downloads model pricing and model parameters at start-up, and exits if that download
fails on a fresh install. The demo and dev images point Bifrost at these local files instead
(`framework.pricing` in their `config.json`), so they start without internet access.

The files are empty on purpose: the demo only uses a mock provider, so there is nothing to
price. For a real deployment, leave `framework.pricing` out to use Bifrost's live data, or point
it at your own copies with `file://` URLs.
