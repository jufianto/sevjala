# Manual smoke tests

Checks that CI cannot run: they need root, a service manager, or real machines. Paste each
transcript into the milestone's PR. Pending ones are listed in [PROGRESS.md](./PROGRESS.md).

## P0 — service install and status

Exit criterion: `sevjala service install && sevjala service status` works on Windows 7,
Windows 11, macOS and Linux.

### Build the binaries (on the Mac)

```sh
make build        # ./sevjala for this machine
make build-all    # dist/: sevjala-{windows,win7}-*.exe, -darwin-*, -linux-amd64, checksums.txt
```

### macOS / Linux

```sh
sudo ./sevjala service install --role node   # prints the status block, exit 0
./sevjala service status                     # see "Expected" below; exit 0
sudo ./sevjala doctor                        # four "ok" lines, exit 0
sudo ./sevjala service stop
./sevjala service status; echo "exit=$?"     # service stopped, agent not answering, exit=1
sudo ./sevjala service start                 # status block again, exit 0
sudo ./sevjala service uninstall             # says "data kept in …"
```

- Without sudo, `status` can show `service unknown (run with sudo for the service state)`
  while the agent answers; that still exits 0. Members of the `sevjala` group reach the socket
  without sudo after logging out and back in (install adds `$SUDO_USER` to the group).
- Data dir: macOS `/Library/Application Support/Sevjala/`, Linux `/var/lib/sevjala/`.
  Log: `<data dir>/logs/sevjala.log` (JSON lines; check `service_installed`, `agent_started`,
  `agent_stopped`, `service_uninstalled`).
- Cleanup after the test: `sudo rm -r` the data dir. The `sevjala` group stays.

### Windows 7 VM (and Windows 11)

VM: Windows 7 SP1 with KB3125574, bridged network (M1 needs it), on the Windows 11 PC. Copy
`dist/sevjala-win7-amd64.exe` (and `sevjala-win7-386.exe` on a 32-bit VM; use
`sevjala-windows-amd64.exe` on Windows 11). From an **Administrator** command prompt:

```bat
sevjala-win7-amd64.exe version
sevjala-win7-amd64.exe service install --role node
sevjala-win7-amd64.exe service status
sevjala-win7-amd64.exe doctor
sevjala-win7-amd64.exe service stop
sevjala-win7-amd64.exe service status
echo exit=%ERRORLEVEL%
sevjala-win7-amd64.exe service start
sevjala-win7-amd64.exe service uninstall
```

- `status` after `stop` must exit 1; every other command exits 0.
- A non-admin prompt must refuse `install` with `run this from an Administrator prompt`
  (this is the check that was broken on Windows 7 before the fix in `svc/admin_windows.go`).
- Data dir `C:\ProgramData\Sevjala\`; the service shows as "Sevjala Agent" in `services.msc`.

### Expected status block

```
service   running      (windows-service)
agent     answering    v0.1.0  role=node  up 2m13s  pid 4120
data dir  C:\ProgramData\Sevjala
```

The platform in brackets is `darwin-launchd` on macOS and `linux-systemd` on Linux.
