import sys
from pathlib import Path

# Tests import the service as the `app` package, same as uvicorn does.
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
