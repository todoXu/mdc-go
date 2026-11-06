# 编译单文件程序到 dist/。程序已内嵌前端、默认配置、内置插件、演员映射表、水印和人脸模型，
# 拷贝这一个文件就能运行；data 和 plugins 会在程序所在目录自动生成。
#
# 用法（在项目根目录）：
#   powershell -ExecutionPolicy Bypass -File tools\build.ps1            # Windows x64 + Linux x64
#   powershell -ExecutionPolicy Bypass -File tools\build.ps1 linux/arm64 # 只编译指定平台
param([string[]]$Targets = @('windows/amd64', 'linux/amd64'))

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null

$saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
try {
    foreach ($target in $Targets) {
        $os, $arch = $target.Split('/')
        $name = "mdc-go-$os-$arch" + $(if ($os -eq 'windows') { '.exe' } else { '' })
        $out = Join-Path $dist $name
        $env:GOOS = $os; $env:GOARCH = $arch; $env:CGO_ENABLED = '0'
        Push-Location $root
        try {
            # -s -w 去掉调试信息，文件更小；-trimpath 不记录本机源码路径。
            go build -trimpath -ldflags '-s -w' -o $out ./cmd/mdc-go
            if ($LASTEXITCODE -ne 0) { throw "编译 $target 失败" }
        } finally { Pop-Location }
        $size = [math]::Round((Get-Item $out).Length / 1MB, 1)
        Write-Host ("{0,-26} {1,6} MB" -f $name, $size)
    }
} finally {
    foreach ($key in $saved.Keys) {
        if ($null -eq $saved[$key]) { Remove-Item "Env:$key" -ErrorAction SilentlyContinue } else { Set-Item "Env:$key" $saved[$key] }
    }
}
Write-Host "输出目录：$dist"
