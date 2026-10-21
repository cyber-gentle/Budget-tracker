@echo off
setlocal

echo ========================================================
echo   Starting Spendly Budget Tracker (Local Server)
echo ========================================================
echo.

:: Check if go is available in PATH
where go >nul 2>nul
if %errorlevel% equ 0 (
    echo [✓] Go compiler found on system.
    echo [*] Starting server on http://localhost:8080 ...
    echo [*] Press Ctrl+C to stop the server at any time.
    echo.
    go run .
    goto :eof
)

:: Search common Go installation directories
if exist "C:\Program Files\Go\bin\go.exe" (
    echo [✓] Go compiler located at C:\Program Files\Go\bin\go.exe
    echo [*] Starting server on http://localhost:8080 ...
    "C:\Program Files\Go\bin\go.exe" run .
    goto :eof
)

if exist "C:\Go\bin\go.exe" (
    echo [✓] Go compiler located at C:\Go\bin\go.exe
    echo [*] Starting server on http://localhost:8080 ...
    "C:\Go\bin\go.exe" run .
    goto :eof
)

:: If go is not found
echo [!] Go was not found on your system PATH.
echo.
echo To run Spendly locally on http://localhost:8080:
echo.
echo 1) Install Go:
echo    Run in PowerShell: winget install GoLang.Go
echo    Or download from: https://go.dev/dl/
echo.
echo 2) Or if using Docker:
echo    docker build -t spendly .
echo    docker run -p 8080:8080 spendly
echo.
pause
