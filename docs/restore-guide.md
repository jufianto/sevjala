# Restoring a database

How to get a database back from a Sevjala backup. Commands run on the **controller** (the PC
with MySQL/MariaDB) from an ordinary Command Prompt; no administrator rights are needed, except
for `restore --file` and `backups export`, which ask for them themselves (Windows shows the UAC prompt, macOS/Linux ask
for your password through `sudo`). `sevjala status` or the tray icon shows first whether the backups
are in good shape.
Examples use a database called `shop`; use yours.

MySQL must be running (XAMPP Control Panel → MySQL → Start).

## 1. Find the backup you want

```bat
sevjala backups list
```

Each line is one backup: its id (`BK-20261016-101500-A8F2` = made 2026-10-16 at 10:15:00),
the database, when it was made, its size and how many backup PCs hold a checked copy.
Pick the newest one made **before** the problem happened. `latest` means the newest one.

## 2. The safe way: restore into a new database

This never touches the live database. Use it first, always, unless the live database is gone or
broken beyond use.

```bat
sevjala restore latest
sevjala restore BK-20261016-101500-A8F2
sevjala restore BK-20261016-101500-A8F2 --into shop_check
```

Sevjala checks the file is not damaged, creates a new database (by default
`shop_restored_<date>`, or the name after `--into`) and loads the backup into it:

```
Restoring BK-20261016-101500-A8F2 (shop) into shop_restored_20261016_1015
  checking the controller's own copy…
  loading into shop_restored_20261016_1015… 42% (4.5 MiB of 10.8 MiB)
Restored BK-20261016-101500-A8F2 into shop_restored_20261016_1015: 21 tables, in 8s, from the controller's own copy.
The live database shop was not touched.
```

Then open phpMyAdmin (`http://localhost/phpmyadmin`), look at the new database, and either copy
the rows you lost back into `shop`, or point your application at the new database. Drop the new
database in phpMyAdmin when you no longer need it.

With several databases backed up, say which one: `sevjala restore latest --db shop`.

## 3. Replace the live database

Only when the live database is broken and you want it exactly as it was in a backup.

1. Stop the application that writes to it (XAMPP Control Panel → Apache → Stop), so nobody
   writes while it is replaced.
2. Run:

   ```bat
   sevjala restore BK-20261016-101500-A8F2 --replace-production
   ```

3. Sevjala shows which backup, when it was made and its size, and asks you to type the database
   name. Type it exactly (`shop`) and press Enter. Anything else cancels and changes nothing.
4. Sevjala first makes a fresh backup of the live database as it is now (labelled `pre-restore`
   in `sevjala backups list`; the newest 5 per database are kept), then drops it and loads
   the backup. The database is unavailable until loading ends.

   ```
   Replaced shop with BK-20261016-101500-A8F2: 21 tables, in 6s, from the controller's own copy.
   The backup made just before is BK-20261016-142233-7C10; to undo: sevjala restore BK-20261016-142233-7C10 --replace-production
   ```

   Write down that undo line: it puts the database back as it was just before.
5. Start Apache again and check the application.

**If it fails half way** (MySQL stopped, disk full, …), the message says the database is only
partly loaded and gives one command, for example:

```
sevjala restore BK-20261016-142233-7C10 --replace-production
```

Fix the cause (start MySQL, free space) and run exactly that command. Until it succeeds,
Sevjala refuses to back up that database, so no backup ever holds the half-loaded state; this
stays true after a restart.

## 4. The controller PC is dead

The backup PCs still hold the backups, and a new PC can be restored from a file.

1. On a backup PC, copy the newest backup out to a USB stick or a shared
   folder (it asks for administrator rights):

   ```bat
   sevjala backups export latest --db shop --to E:\sevjala-export
   ```

   This writes `E:\sevjala-export\shop\<date>.sql.gz` and a `.json` beside it. Keep both.
2. On the new server PC: install XAMPP and start MySQL, install Sevjala (`sevjala setup`, choose
   controller), then connect it to MySQL without choosing a database yet:

   ```bat
   sevjala db set --no-password
   ```

   (or `--password` if MySQL has one).
3. Restore the file under the database's own name:

   ```bat
   sevjala restore --file E:\sevjala-export\shop\2026-10-16_10-15-00.sql.gz --into shop
   ```

   This one asks for administrator rights (the service reads the file you name). The file is
   checked against its `.json` first; a damaged file is refused before anything is
   created.
4. Back it up again from now on: `sevjala db set --db shop`, then pair the backup PCs with the
   new controller (`sevjala discover`, `sevjala pair`).

## Watching, and how long it takes

- Ctrl-C only stops watching; the restore carries on. `sevjala restore --status` shows it again.
- One restore runs at a time.
- Loading is slower than backing up: a 45 MB database loads in about 8 s; allow a few minutes
  for 500 MB.
- If the newest copy is not on the controller any more, Sevjala fetches it from a backup PC that
  is on, and checks it the same way. A damaged copy is skipped and the next one is used.

## When something says no

| Message | What to do |
|---|---|
| `database shop_check already exists` | Choose another name with `--into`, or drop the old one in phpMyAdmin. |
| `several databases are backed up (…): add --db NAME` | Add `--db shop`. |
| `no backup of shop to restore` | No backup with a readable copy: check `sevjala backups list` and that the backup PCs are on. |
| `MySQL is not running or not reachable` | Start MySQL in the XAMPP Control Panel. |
| `a restore is already running` | Wait for it: `sevjala restore --status`. |
| `a backup of shop is running` | Wait a minute and try again. |
| `damaged: its size or sha256 does not match` | That copy is broken. Use another backup, or (for `--file`) export the file again. |
| `this agent is a node` | You are on a backup PC. Use step 4: export the file and restore it on the controller. |

Good to know: a restored copy keeps the database's triggers, views and stored procedures as
they were. One that names the original database explicitly (`shop.orders` instead of `orders`)
still points at the original from inside the copy.
