"""Dryad: Flask gateway to the team's Plant API."""
import io
import os
import re
from pathlib import Path
from urllib.parse import urlsplit

import qrcode
import requests
from dotenv import load_dotenv
from flask import Flask, Response, jsonify, render_template, request, send_file

load_dotenv(Path(__file__).with_name('.env'))
app = Flask(__name__)
app.config['MAX_CONTENT_LENGTH'] = 10 * 1024 * 1024  # a 20-second voice recording is far smaller
BASE = os.getenv('BACKEND_URL', 'http://127.0.0.1:8080').rstrip('/')
TOKEN = os.getenv('BACKEND_TOKEN', '')
if urlsplit(BASE).scheme not in ('http', 'https') or not urlsplit(BASE).hostname:
    raise ValueError('BACKEND_URL must be an HTTP or HTTPS base URL')


def upstream(path, recording=None, content_type=None, payload=None):
    """Fixed GET routes, plus POSTing a voice recording to talk to a plant or
    a caretaker's wallet (payload, as JSON); sensor writes are intentionally absent."""
    try:
        auth = {'Authorization': 'Bearer ' + TOKEN} if TOKEN else {}
        if payload is not None:
            response = requests.post(BASE + path, json=payload, headers=auth, timeout=(3, 4), allow_redirects=False)
        elif recording is None:
            response = requests.get(BASE + path, headers=auth, timeout=(3, 4), allow_redirects=False)
        else:
            response = requests.post(BASE + path, data=recording,
                headers={**auth, 'Content-Type': content_type}, timeout=(3, 10), allow_redirects=False)
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
    response.headers['Permissions-Policy'] = 'camera=(self), microphone=(self)'
    return response


@app.errorhandler(413)
def too_large(error):
    return jsonify(error='That recording is too long.'), 413


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


def valid_wallet(wallet):
    # A Solana public address is Base58 encoding of exactly 32 bytes.
    alphabet = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
    if not isinstance(wallet, str) or not 32 <= len(wallet) <= 44:
        return False
    value = 0
    for char in wallet:
        if char not in alphabet:
            return False
        value = value * 58 + alphabet.index(char)
    leading = len(wallet) - len(wallet.lstrip('1'))
    return value != 0 and leading + (value.bit_length() + 7) // 8 == 32


@app.route('/api/caretaker', methods=['GET', 'POST'])
def caretaker():
    if request.method == 'GET':
        return upstream('/api/caretaker')
    # A custom header plus JSON requires a cross-origin browser preflight.
    # This app does not grant CORS access. Works behind HTTPS tunnels too.
    if (request.headers.get('X-Dryad-Request') != 'caretaker'
            or request.headers.get('Sec-Fetch-Site') == 'cross-site'):
        return jsonify(error='Save the wallet from the Dryad website.'), 403
    if not request.is_json:
        return jsonify(error='Send a JSON wallet and plant_id.'), 415
    body = request.get_json(silent=True)
    if not isinstance(body, dict) or set(body) != {'wallet', 'plant_id'}:
        return jsonify(error='Send only wallet and plant_id.'), 400
    wallet = body['wallet'].strip() if isinstance(body['wallet'], str) else ''
    plant_id = body['plant_id']
    if not valid_wallet(wallet):
        return jsonify(error='Enter a valid Solana public wallet address.'), 400
    if not isinstance(plant_id, str) or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', plant_id):
        return jsonify(error='Choose a plant first.'), 400
    return upstream('/api/caretaker', payload={'wallet': wallet, 'plant_id': plant_id})


@app.get('/api/backend-health')
def backend_health():
    return upstream('/healthz')


@app.post('/api/talk/<plant_id>')
def talk(plant_id):
    # A voice recording from the page. The plant API queues it and returns a
    # job, which the page asks about until the answer is ready.
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,64}', plant_id):
        return jsonify(error='Invalid plant ID'), 400
    recording = request.get_data()
    if not recording:
        return jsonify(error='The recording was empty. Try again.'), 400
    return upstream('/api/talk/' + plant_id, recording, request.content_type or 'application/octet-stream')


@app.get('/api/talk/jobs/<job_id>')
def talk_job(job_id):
    if not re.fullmatch(r'[0-9a-f]{32}', job_id):
        return jsonify(error='Invalid answer ID'), 400
    return upstream('/api/talk/jobs/' + job_id)


@app.get('/api/talk/jobs/<job_id>/voice')
def talk_voice(job_id):
    # The plant's answer as MP3; errors stay JSON.
    if not re.fullmatch(r'[0-9a-f]{32}', job_id):
        return jsonify(error='Invalid answer ID'), 400
    try:
        response = requests.get(BASE + '/api/talk/jobs/' + job_id + '/voice',
            headers={'Authorization': 'Bearer ' + TOKEN} if TOKEN else {},
            timeout=(3, 10), allow_redirects=False)
    except requests.RequestException:
        return jsonify(error='Plant API unavailable. Check BACKEND_URL and the sensor server.'), 503
    if response.status_code != 200:
        try:
            return jsonify(response.json()), response.status_code
        except ValueError:
            return jsonify(error='The plant API returned invalid JSON.'), 502
    return Response(response.content, mimetype='audio/mpeg')


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
