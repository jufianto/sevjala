# Test walkthrough: one Windows PC and one Mac

A step-by-step run of the real product on two machines: a Windows PC with XAMPP as the
**controller** and a Mac as the backup **node**. They can be on the same LAN or joined by
Tailscale. It covers install, pairing, the first backup, the schedule and a node going
offline in about two hours. The full per-milestone checks are in
[smoke-tests.md](./smoke-tests.md).

Fill in your own values once:

| Placeholder | What it is | How to find it |
|---|---|---|
| `<win-ip>` | the Windows PC's address | `tailscale ip -4` on it, or `ipconfig` on a LAN |
| `<mac-ip>` | the Mac's address | `tailscale ip -4` on the Mac, or `ipconfig getifaddr en0` |
| `<node-id>` | the Mac's 8-character agent id | printed by `sudo sevjala code` (step 5) |

Write down what each step prints next to its **Expect** line. Anything different is a
finding: copy the whole output.

## 1. Open an Administrator prompt (Windows)

Every `sevjala` command on Windows runs from an Administrator prompt: Start → type `cmd` →
right-click **Command Prompt** → **Run as administrator** → Yes.

In a normal prompt the agent refuses on purpose (only Administrators may talk to it). Builds
up to v0.3.0-alpha.1 print `… \\.\pipe\sevjala: Access is denied.`; later builds print
`refused this user: run this from an Administrator prompt`.

## 2. Check the controller (Windows)

Skip the install if setup already ran. Otherwise double-click `sevjala-win7-amd64.exe` and
choose **1** (see the Installer section of [smoke-tests.md](./smoke-tests.md)).

```bat
sevjala version
sevjala service status
sevjala doctor
```

**Expect:** `service running`, `agent answering … role=controller`. In `doctor`, every line
is `ok` except `database` (MySQL not started yet), `schedule` (no database chosen) and
`nodes` (none paired).

## 3. Start MySQL (Windows)

Open the **XAMPP Control Panel**, click **Start** next to MySQL, and tick the box left of it
(**Service**) so MySQL starts with Windows, as it would in an office.

**Expect:** MySQL turns green. `tasklist | findstr mysqld` shows `mysqld.exe`.

## 4. Choose the database (Windows)

```bat
sevjala db detect
sevjala db set --db <your database> --no-password
```

Use `--password` instead of `--no-password` if MySQL's root user has one (XAMPP's default
has none); it is asked for and not shown.

**Expect:** `db detect` lists `C:\xampp\mysql\bin\mysqldump.exe` and your databases.
`db set` says it is saved. `sevjala doctor` now shows `ok database` and
`ok schedule (next run …)`.

A wrong password must say "not saved" or "Access denied" and keep the old setting.

## 5. Make the Mac a node (Mac, Terminal)

```sh
curl -L -o sevjala https://github.com/jufianto/sevjala/releases/latest/download/sevjala-darwin-arm64
chmod +x sevjala && sudo mv sevjala /usr/local/bin/
sudo sevjala setup              # choose 2 (node)
```

On an Intel Mac, download `sevjala-darwin-amd64`. While v0.3.0-alpha.1 is a pre-release,
`latest` does not point to it: use
`https://github.com/jufianto/sevjala/releases/download/v0.3.0-alpha.1/sevjala-darwin-arm64`.

**Expect:** the service is installed and a pairing block is printed: an 8-character id, a
6-digit code (valid 10 minutes) and a fingerprint. A new code: `sudo sevjala code`.

## 6. Pair (Windows)

Discovery only finds nodes on the same LAN segment. Over Tailscale, add the Mac by address
first:

```bat
sevjala agents add <mac-ip>
sevjala pair <node-id> --code <code> --fp <fingerprint>
sevjala ping <node-id>
sevjala agents
```

On the same LAN, `sevjala discover` lists the Mac instead (`PAIRED no`), and `agents add`
is not needed.

**Expect:** `paired with …`, then `answered in N ms … healthy`; `agents` shows the Mac
`online` with FREE filled in.

Also try once: a wrong code → "wrong pairing code"; a wrong fingerprint → "do not pair".
Neither may pair.

## 7. First backup (Windows, then Mac)

```bat
sevjala backup now
sevjala backups list --matrix
```

**Expect:** `dumping…`, `created`, then `verified` for the Mac; the matrix shows `v`.

On the Mac, check the file arrived intact:

```sh
sudo ls -l "/Library/Application Support/Sevjala/store"
sudo shasum -a 256 "/Library/Application Support/Sevjala/store/"BK-*.sql.gz
```

**Expect:** one `BK-….sql.gz` and its `.json`; the hash equals the `sha256` in the `.json`
and in `sevjala backups list` on Windows.

Copy the newest one out without sudo (setup added you to the `sevjala` group; log out and
back in once after the first install):

```sh
sevjala backups list
sevjala backups export latest --to ~/Desktop
ls ~/Desktop/<your database>
```

**Expect:** `backups list` shows what the Mac holds; `export` prints "sha256 checked" and the
path; `~/Desktop/<your database>/` has `2026-…_hh-mm-ss.sql.gz` and `.json`, owned by you. Running the
same export again refuses: "already exists; add --force". `sevjala backups export all --to ~/Desktop`
copies the rest and reports the latest one as skipped. On Windows the same command copies
from staging (Administrator prompt).

## 8. Leave it running (about 1 hour)

```bat
sevjala policy list
```

**Expect:** `frequent`, `hourly`, `daily`, `monthly`, `manual`, with NEXT RUN filled in.
Wait an hour, then `sevjala backups list --matrix`: a `frequent` backup every 10 minutes and
one `hourly`, all `v`. `sevjala retention run --dry-run` prints "nothing to do".

## 9. Node offline and back (Mac)

```sh
sudo sevjala service stop
```

Wait 30 minutes. On Windows, `sevjala agents` shows the Mac `offline`; backups keep being
made and show as not yet copied in its column.

```sh
sudo sevjala service start
```

**Expect:** within about 10 minutes the missing backups arrive (`backfill_queued` in the
controller log, `C:\ProgramData\Sevjala\logs\sevjala.log`), and `backups list --matrix` is
all `v` again.

## 10. Run setup again (Windows)

Double-click the downloaded `.exe` again and accept UAC.

**Expect:** it shows the service status and a menu: Update, Pair a node, Uninstall, Exit.
Choose **Exit**. Do not uninstall yet if you want to keep testing.

## 11. Clean up

```sh
sevjala agents remove <node-id>        # Windows: stop backing up to the Mac (its files stay)
sudo sevjala service uninstall          # Mac
```

On Windows, run setup again and choose **Uninstall**, or keep it. Backups and settings stay
in `C:\ProgramData\Sevjala` and `/Library/Application Support/Sevjala` either way. Delete
those folders by hand for a fresh start.

## When something fails

Send the command, its full output, and the end of the log on that machine:

- Windows: `C:\ProgramData\Sevjala\logs\sevjala.log`
- Mac: `/Library/Application Support/Sevjala/logs/sevjala.log`

`sevjala doctor` on both machines is the quickest first check.

## Optional: let Claude drive the Windows PC over SSH

With OpenSSH Server enabled on Windows and your Mac's public key in
`C:\ProgramData\ssh\administrators_authorized_keys`, `ssh <user>@<win-ip>` from the Mac
opens an Administrator `cmd` session (`whoami` gives the user). Claude Code can then run
steps 2, 4 and 6 to 9 itself. Steps 1, 3 and 10 need someone at the screen.
