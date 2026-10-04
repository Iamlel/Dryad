"""Tests for the website's gateway (app.py), with the Go API mocked out."""
import sys
import unittest
from pathlib import Path
from unittest.mock import Mock, patch
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
import app

class DryadGatewayTests(unittest.TestCase):
    def setUp(self):self.client=app.app.test_client()
    def test_documented_get_routes(self):
        for local,remote,payload in [('/api/plants','/api/plants',[{'id':'fern','name':'Fern'}]),('/api/plant/fern','/api/plant/fern',{'moisture_pct':None,'light_pct':0,'temperature_c':22,'status':'dark','stale':False,'dialog':'Hello','updated_at':'2026-10-04T00:20:47Z'}),('/api/sensors','/api/sensors',{'seq':10}),('/api/backend-health','/healthz',{'status':'ok'}),('/api/plant','/api/plant',{'status':'ok'})]:
            with self.subTest(path=local),patch.object(app.requests,'get',return_value=Mock(status_code=200,json=lambda:payload)) as get:
                r=self.client.get(local)
                self.assertEqual(r.json,payload)
                self.assertEqual(get.call_args.args[0],app.BASE+remote)
    def test_errors_and_no_fake_fallback(self):
        with patch.object(app.requests,'get',return_value=Mock(status_code=503,json=lambda:{'error':'no sensor reading yet'})):
            r=self.client.get('/api/plant/fern');self.assertEqual(r.status_code,503);self.assertEqual(r.json['error'],'no sensor reading yet')
        with patch.object(app.requests,'get',side_effect=app.requests.Timeout):
            self.assertEqual(self.client.get('/api/plants').status_code,503)
        with patch.object(app.requests,'get',return_value=Mock(status_code=200,json=Mock(side_effect=ValueError))):
            self.assertEqual(self.client.get('/api/plants').status_code,502)
    def test_removed_routes(self):
        for route in ['chat','assessment','audio','history','readings']:
            self.assertEqual(self.client.post('/api/plants/fern/'+route).status_code,404)
        self.assertEqual(self.client.post('/api/sensors',json={'moisture_pct':20}).status_code,405)
    def test_template_and_qr(self):
        r=self.client.get('/');self.assertEqual(r.status_code,200)
        self.assertNotIn(b'chat-form',r.data);self.assertNotIn(b'Groot',r.data)
        self.assertEqual(self.client.get('/qr/fern.png').mimetype,'image/png')
        self.assertEqual(self.client.get('/healthz').json['status'],'ok')
if __name__=='__main__':unittest.main()
