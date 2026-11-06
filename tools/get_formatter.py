"""Fetch only standalone formatting tools; no frontend runtime dependencies."""
import base64
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

root = Path(__file__).resolve().parents[1]
with urllib.request.urlopen('https://registry.npmjs.org/prettier/latest', timeout=30) as response:
    package = json.load(response)
with urllib.request.urlopen(package['dist']['tarball'], timeout=30) as response:
    data = response.read()
assert package['dist']['integrity'] == 'sha512-' + base64.b64encode(hashlib.sha512(data).digest()).decode()
files = ['standalone.mjs', 'plugins/babel.mjs', 'plugins/estree.mjs', 'plugins/postcss.mjs', 'plugins/html.mjs', 'LICENSE']
with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
    for name in files:
        target = root / '.tools/prettier' / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(archive.extractfile('package/' + name).read())
print('Development formatter ready:', package['version'])
