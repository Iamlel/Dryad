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


def upstream(path, recording=None, content_type=None):
    """Fixed GET routes, plus POSTing a voice recording to talk to a plant;
    sensor writes are intentionally absent."""
    try:
        auth = {'Authorization': 'Bearer ' + TOKEN} if TOKEN else {}
        if recording is None:
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
