"""Remove deployment-specific URLs from the inherited sample configuration."""
import ipaddress
import json
from pathlib import Path
from urllib.parse import urlsplit

path = Path(__file__).resolve().parents[1] / 'internal/server/assets/defaults.json'
data = json.loads(path.read_text(encoding='utf-8'))

def clean(value):
    if isinstance(value, dict):
        return {key: clean(item) for key, item in value.items()}
    if isinstance(value, list):
        return [clean(item) for item in value]
    if isinstance(value, str) and '://' in value:
        parsed = urlsplit(value)
        if parsed.username or parsed.password or parsed.query or parsed.hostname == 'localhost':
            return ''
        try:
            if not ipaddress.ip_address(parsed.hostname).is_global:
                return ''
        except ValueError:
            pass
    return value

path.write_text(json.dumps(clean(data), ensure_ascii=False, indent=2), encoding='utf-8')
