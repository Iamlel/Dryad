# Dryad update — replace only these files

Extract this ZIP into your EXISTING website folder (the folder with app.py).
Allow replacements. Keep your existing `static/fonts/`, `static/vendor/`,
virtual environment and launcher. Do NOT delete the old project folder.
This is an update pack, not a standalone full project.

Replacements:
- app.py
- templates/index.html
- static/app.js
- static/style.css
- requirements.txt
- .env.example (a template only; your actual .env is not overwritten)

New files:
- static/dryad.svg — mythical woodland spirit artwork
- serve.py — production server entrypoint
- hosting/Caddyfile.example — optional HTTPS reverse-proxy example
- tests/test_dryad_gateway.py — tests for the new API contract
- this guide

The old static/groot.svg and teammate/ bridge files are no longer used and may
be deleted. The old README and tests describe the previous Groot app; use this
guide instead and run only the new test file named below. The real Go app now
already provides the required API: do NOT install the previously supplied bridge.

## Start locally in your Bash / UCRT64 terminal

Stop Flask with Ctrl+C. From your existing project folder:

```bash
source .venv/Scripts/activate
python -m pip install -r requirements.txt
python app.py
```

Open http://localhost:5000 and refresh the browser after the update. The existing
Windows launcher also continues to work. This is now LIVE ONLY. It does not
fabricate data when the API is unavailable. Old GROOT_MODE settings are ignored.

Edit the existing `.env` on the computer running Flask:

```dotenv
BACKEND_URL=http://127.0.0.1:8080
BACKEND_TOKEN=
HOST=127.0.0.1
PORT=5000
```

Use 127.0.0.1 ONLY if Go and Flask run on the same computer. If Go runs on your
friend's laptop, use that laptop's currently reachable IP, for example the address
you supplied, `http://10.196.134.197:8080`, only if that is still correct and
reachable from the Flask machine. It is a private LAN address, not a public domain.
Leave BACKEND_TOKEN empty unless the actual API host requires a bearer token;
an old token from the previous bridge is not required by the supplied contract.
Restart Flask after environment changes. Never send actual secrets in the ZIP.

## What changed

- Dryad branding and woodland spirit replace the Groot artwork.
- AI ratings, AI requests, chat, dictation, microphone and recording are removed.
- The page shows the actual API's `status` and `dialog`, including its placeholder
  dialog text. It never claims those words are AI-generated.
- GET /api/plants is handled as a bare list. IDs select GET /api/plant/<id>.
- Readings use moisture_pct, light_pct, temperature_c, updated_at and stale.
- Refreshes every 1.5 seconds after each completed request. Failed requests retry;
  slow requests time out. The roster refreshes every 30 seconds and on Refresh.
- Null sensor values display —, not zero. Real zero remains zero.
- The API's most urgent status is preserved. Independent visual effects use the
  selected plant's thresholds: night below light_min_pct, dry/wet outside moisture
  range, and hot/cold outside temperature range. Effects can combine.
- API stale/offline flags or timestamps older than 10 seconds mark data stale.
- All plants share the sensor readings, as documented by your backend; their
  thresholds, status and dialog differ. No separate sensors are invented.
- Graphs retain up to 100 fresh unique samples per selected plant in this tab's
  memory. They start empty and reset on reload. No nonexistent history endpoint
  is called. Missing values and connection gaps break graph lines.
- New QR labels encode `dryad:fern`. Old `groot:fern` labels still scan. Raw IDs,
  JSON {"plant_id":"fern"}, and URLs containing ?plant=fern are also accepted.
- QR contents are matched against the plant list, not opened as arbitrary links.
- The website does not expose POST /api/sensors or any plant-editing endpoint.

## Give your friend the updated website for hosting

After applying this update, ZIP and send the COMPLETE project containing app.py,
serve.py, requirements.txt, templates/, static/ (including fonts and vendor),
.env.example, and this guide. Exclude .venv, __pycache__, real .env, old previews,
tests and the old teammate/ bridge. The update ZIP alone relies on existing fonts
and the bundled QR decoder, so it is not sufficient for a blank hosting server.

Hosting requirements: Python 3.11+, a host that runs Python processes, and a route
from that host to your plant API. Static-only hosting cannot run this Flask app.
No Node.js or additional Go program is needed for the website.

Install and start:

```sh
python -m pip install -r requirements.txt
python serve.py
```

This uses Waitress on Windows or Linux. Keep it running through your host's
process manager/service. A platform expecting a public container listener needs
HOST=0.0.0.0 and its assigned PORT; a reverse proxy on the same machine should
use HOST=127.0.0.1 and PORT=5000. Backend settings belong in the host's environment.

A custom domain requires DNS and HTTPS; no domain was supplied or deployed by
this update. For a server your friend controls, the optional Caddy example can
proxy HTTPS to Waitress. Replace dryad.example.com with your domain, point DNS
A/AAAA records to that server's reachable address, allow inbound 80/443, and run
Caddy with the example as its configuration. Caddy obtains HTTPS certificates
when the domain and network are configured correctly. If the hosting platform
already manages HTTPS and domains, use its domain setup instead.

Request flow: phone -> https://your-domain -> Flask -> BACKEND_URL -> Go API.
The phone never connects directly to localhost:8080. Camera scanning works on
HTTPS or localhost with permission; a plain HTTP LAN URL on your phone may block
camera access. Manual QR entry still works. If Go stays on a private laptop and
the website is hosted elsewhere, your friend must establish a reachable private
connection or authenticated HTTPS endpoint; entering a domain name alone does
not make that laptop reachable. No public sensor write route is added here.

Diagnostics:
- /healthz: is the website running?
- /api/backend-health: proxy of the plant API's /healthz
- /api/sensors: read-only raw sensor diagnostics
- /api/plants: plant database/list endpoint
Readable API error messages (400/404/409/503) are passed through to the page.
A healthy /healthz on the Go service does not guarantee its plant DB is reachable.

## Verification

```bash
python -m unittest discover -s tests -p 'test_dryad_gateway.py'
```

Tested with the supplied API contract using fixtures. The actual private sensor
server and your domain still need a live connectivity check on the hosting machine.

Official hosting references:
https://docs.pylonsproject.org/projects/waitress/en/stable/usage.html
https://flask.palletsprojects.com/en/stable/deploying/
https://caddyserver.com/docs/quick-starts/https
