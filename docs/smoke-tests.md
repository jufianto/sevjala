# Manual smoke tests

Checks that CI cannot run: they need root, a service manager, or real machines (P0, M1). Paste each
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

## M1 — discover, pair, ping across two PCs

Exit criterion: discover → pair → ping across two real PCs; an unpaired peer is rejected at TLS.

Setup: PC A (controller) and PC B (node) on the same LAN (the Win7 VM needs **bridged**
networking). A third machine C as a stranger controller is optional but proves the rejection.
Run every command from an Administrator prompt / with sudo.

```sh
# A
sevjala service install --role controller
# B
sevjala service install --role node
# A
sevjala discover                      # B listed, PAIRED no
# B
sevjala code                          # note the code and B's 8-char id
# A
sevjala pair <B id or hostname> --code 000000   # wrong code → "wrong pairing code", exit 1
sevjala pair <B id or hostname> --code <code>   # "paired with B (…) sha256:…"
sevjala ping <B>                      # "answered in N ms: node …, healthy"
sevjala agents                        # B online, LAST SEEN now
sevjala discover                      # B listed, PAIRED yes
# C (stranger controller)
sevjala service install --role controller
sevjala agents add <B ip>
sevjala pair <B> --code 123456        # "rejected this controller's certificate", exit 1
# B: restart the service, then on A
sevjala ping <B>                      # still answers
```

Windows checks on each Windows PC:

- `sevjala doctor` shows `ok lockdown`, `ok lan api`, `ok firewall`.
- `icacls C:\ProgramData\Sevjala` lists only `NT AUTHORITY\SYSTEM` and `BUILTIN\Administrators`.
- `netsh advfirewall firewall show rule name=Sevjala-LAN-API` and `…name=Sevjala-Discovery` exist;
  after `sevjala service uninstall` both are gone.

macOS/Linux: `sudo sevjala doctor` shows `ok lockdown` (`ls -ld` on the data dir: `drwx------ root`).

Logs on B (`<data dir>/logs/sevjala.log`) contain `pair_code_issued`, `pair_rejected`,
`pair_succeeded`, `peer_rejected` and never the code itself.
