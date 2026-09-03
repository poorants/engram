import sys
from pathlib import Path

# The app modules import each other by name (``from core import ...``) because
# they run as ``app.web`` inside the container with app/ on the path. Tests get
# the same view rather than a different one, so what they exercise is what runs.
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))
