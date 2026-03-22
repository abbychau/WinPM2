@echo off
setlocal

for %%I in ("%CD%") do set "FOLDER_EXE=%%~nI.exe"
set "SOURCE_EXE="

echo [0/4] Saving current process list if daemon is running
if exist "C:\bin\winpm2.exe" (
  "C:\bin\winpm2.exe" save >nul 2>&1
)

echo [1/4] Building with go build .
go build .
if errorlevel 1 (
  echo Build failed.
  exit /b 1
)

if exist "winpm2.exe" (
  set "SOURCE_EXE=winpm2.exe"
) else if exist "%FOLDER_EXE%" (
  set "SOURCE_EXE=%FOLDER_EXE%"
) else (
  echo Could not find built executable. Expected winpm2.exe or %FOLDER_EXE%.
  exit /b 1
)

echo [2/4] Stopping running winpm2 processes
taskkill /IM winpm2.exe /F >nul 2>&1
taskkill /IM "%FOLDER_EXE%" /F >nul 2>&1

echo [3/4] Copying binary to C:\bin\winpm2.exe
if not exist "C:\bin" (
  echo C:\bin does not exist.
  exit /b 1
)
copy /Y "%SOURCE_EXE%" "C:\bin\winpm2.exe" >nul
if errorlevel 1 (
  echo Copy failed from %SOURCE_EXE% to C:\bin\winpm2.exe.
  exit /b 1
)

echo [4/4] Bringing winpm2 back up
"C:\bin\winpm2.exe" list >nul

echo Done.
endlocal
