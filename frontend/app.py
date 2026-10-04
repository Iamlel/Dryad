"""Dryad: read-only Flask gateway to the team's Plant API."""
import io
import os
import re
from pathlib import Path
from urllib.parse import urlsplit

import qrcode
import requests
from dotenv import load_dotenv
from flask import Flask, jsonify, render_template, send_file

load_dotenv(Path(__file__).with_name('.env'))
app = Flask(__name__)
BASE = os.getenv('BACKEND_URL', 'http://127.0.0.1:8080').rstrip('/')
TOKEN = os.getenv('BACKEND_TOKEN', '')
if urlsplit(BASE).scheme not in ('http', 'https') or not urlsplit(BASE).hostname:
    raise ValueError('BACKEND_URL must be an HTTP or HTTPS base URL')


def upstream(path):
    """Only fixed GET routes are exposed; sensor writes are intentionally absent."""
    try:
        response = requests.get(BASE + path,
            headers={'Authorization': 'Bearer ' + TOKEN} if TOKEN else {},
            timeout=(3, 4), allow_redirects=False)
        if 300 <= response.status_code < 400:
            return jsonify(error='Backend redirected the request. Set BACKEND_URL to its final address.'), 502
        try:
            payload = response.json()
        except ValueError:
            return jsonify(error='The plant API returned invalid JSON.'), 502
        return jsonify(payload), response.status_code
    except requests.RequestException:
        return jsonify(error='Plant API unavailable. Check BACKEND_URL and the sensor server.'), 503


@app.after_request
def headers(response):
    if not response.mimetype.startswith('image/'):
        response.headers['Cache-Control'] = 'no-store'
    response.headers['X-Content-Type-Options'] = 'nosniff'
    response.headers['Referrer-Policy'] = 'same-origin'
    response.headers['Permissions-Policy'] = 'camera=(self), microphone=()'
    return response


@app.get('/')
def home():
    return render_template('index.html')


@app.get('/api/plants')
def plants():
    # The backend returns a bare ARRAY, not {"plants": [...]}.
    return upstream('/api/plants')


@app.get('/api/plant/<plant_id>')
def plant(plant_id):
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', plant_id):
        return jsonify(error='Invalid plant ID'), 400
    return upstream('/api/plant/' + plant_id)


@app.get('/api/plant')
def generic_plant():
    return upstream('/api/plant')


@app.get('/api/sensors')
def sensors():
    return upstream('/api/sensors')


@app.get('/api/backend-health')
def backend_health():
    return upstream('/healthz')


@app.get('/healthz')
def health():
    # Website liveness is separate from the sensor/database health.
    return jsonify(status='ok', service='dryad-web')


@app.get('/qr/<plant_id>.png')
def qr(plant_id):
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', plant_id):
        return jsonify(error='Invalid plant ID'), 400
    buffer = io.BytesIO()
    qrcode.make('dryad:' + plant_id).save(buffer, format='PNG')
    buffer.seek(0)
    return send_file(buffer, mimetype='image/png')


if __name__ == '__main__':
    app.run(host='127.0.0.1', port=int(os.getenv('PORT', '5000')), debug=False)
