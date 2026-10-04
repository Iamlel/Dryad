"""Production server (Waitress), as the board runs it. HOST and PORT come from
the environment."""
import os
from waitress import serve
from app import app

if __name__ == '__main__':
    serve(app, host=os.getenv('HOST', '127.0.0.1'),
          port=int(os.getenv('PORT', '5000')), threads=8)
