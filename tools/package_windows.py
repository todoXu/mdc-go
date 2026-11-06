"""Build and package the standalone Windows app; all output stays in mdc-go."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import zipfile

root = Path(__file__).resolve().parents[1]
go = shutil.which('go') or str(root.parent / '.tools/go/bin/go.exe')
env = dict(os.environ, GOOS='windows', GOARCH='amd64', CGO_ENABLED='0', GOCACHE=str(root / '.tools/gocache'), GOPATH=str(root / '.tools/gopath'), GOMODCACHE=str(root / '.tools/gomodcache'))
subprocess.run([go, 'build', '-trimpath', '-o', 'mdc-go.exe', './cmd/mdcng'], cwd=root, env=env, check=True)
archive = root / 'mdc-go-windows-x64.zip'
with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as bundle:
    for name in ['mdc-go.exe', 'start.cmd', 'README.md', 'docs/API.md', 'docs/PLUGINS.md', 'docs/PIGO-LICENSE.txt']:
        bundle.write(root / name, 'mdc-go-windows-x64/' + name)
    for plugin in (root / 'internal/provider/defaults').glob('*/*'):
        bundle.write(plugin, 'mdc-go-windows-x64/plugins/' + plugin.parent.name + '/' + plugin.name)
    modules = {
        'goquery': 'github.com/!puerkito!bio/goquery@*/LICENSE',
        'cascadia': 'github.com/andybalholm/cascadia@*/LICENSE',
        'goja': 'github.com/dop251/goja@*/LICENSE',
        'regexp2': 'github.com/dlclark/regexp2/v2@*/LICENSE',
        'sourcemap': 'github.com/go-sourcemap/sourcemap@*/LICENSE',
        'pprof': 'github.com/google/pprof@*/LICENSE',
        'x-net': 'golang.org/x/net@*/LICENSE',
        'x-text': 'golang.org/x/text@*/LICENSE',
        'pigo': 'github.com/esimov/pigo@*/LICENSE',
        'x-image': 'golang.org/x/image@*/LICENSE',
    }
    for title, pattern in modules.items():
        matches = list((root / '.tools/gomodcache').glob(pattern))
        if matches:
            bundle.write(matches[0], 'mdc-go-windows-x64/licenses/' + title + '.txt')
archive.with_suffix('.zip.sha256').write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + archive.name + '\n', encoding='ascii')
print('Windows EXE:', (root / 'mdc-go.exe').stat().st_size, 'bytes')
print('ZIP:', archive, archive.stat().st_size, 'bytes')
