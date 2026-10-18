<?php
// Sevjala testbed: add and list notes, and show which server answered and,
// on a replica, how far behind it is. Runs on PHP 5.6 (XAMPP) and PHP 8.
require __DIR__ . '/config.php';

function h($s)
{
    return htmlspecialchars((string) $s, ENT_QUOTES, 'UTF-8');
}

mysqli_report(MYSQLI_REPORT_OFF);
$db = @new mysqli($cfg['host'], $cfg['user'], $cfg['password'], $cfg['database'], $cfg['port']);
$error = '';
if ($db->connect_errno) {
    $error = 'Cannot connect to ' . $cfg['host'] . ':' . $cfg['port'] . ': ' . $db->connect_error;
} else {
    $db->set_charset('utf8mb4');
}

if (!$error && $_SERVER['REQUEST_METHOD'] === 'POST') {
    $title = trim(isset($_POST['title']) ? $_POST['title'] : '');
    $body = trim(isset($_POST['body']) ? $_POST['body'] : '');
    if ($title === '') {
        $error = 'A title is needed.';
    } else {
        $st = $db->prepare('INSERT INTO notes (title, body) VALUES (?, ?)');
        if ($st && $st->bind_param('ss', $title, $body) && $st->execute()) {
            header('Location: ?added=' . $st->insert_id);
            exit;
        }
        $error = 'Not saved: ' . ($st ? $st->error : $db->error);
    }
}

$server = $replica = null;
$count = $sizeMB = 0;
$rows = array();
$before = isset($_GET['before']) ? (int) $_GET['before'] : 0;
if (!$db->connect_errno) {
    $r = $db->query('SELECT @@hostname AS host, @@server_id AS id, @@read_only AS ro, VERSION() AS version');
    $server = $r ? $r->fetch_assoc() : null;
    $r = $db->query('SHOW SLAVE STATUS');
    $replica = $r ? $r->fetch_assoc() : null;
    $r = $db->query('SELECT COUNT(*) AS n FROM notes');
    if ($r) {
        $count = (int) $r->fetch_object()->n;
    } elseif (!$error) {
        $error = 'No notes table yet: ' . $db->error;
    }
    $r = $db->query('SELECT ROUND(SUM(data_length + index_length) / 1048576) AS mb FROM information_schema.tables WHERE table_schema = DATABASE()');
    $sizeMB = $r ? (int) $r->fetch_object()->mb : 0;
    $sql = 'SELECT id, title, LEFT(body, 60) AS body, created_at FROM notes'
        . ($before > 0 ? ' WHERE id < ' . $before : '') . ' ORDER BY id DESC LIMIT 50';
    $r = $db->query($sql);
    while ($r && ($row = $r->fetch_assoc())) {
        $rows[] = $row;
    }
}
?>
<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sevjala testbed · <?= h($cfg['label']) ?></title>
<style>
  body { font: 15px system-ui, sans-serif; margin: 0 auto; max-width: 900px; padding: 16px; color: #1c2330; }
  .card { border: 1px solid #dde1e7; border-radius: 10px; padding: 12px 16px; margin: 12px 0; }
  .bad { border-color: #c62828; background: #fdecea; }
  .ok { color: #1a7f37; } .warn { color: #a15c00; }
  table { border-collapse: collapse; width: 100%; } td, th { text-align: left; padding: 4px 6px; border-bottom: 1px solid #eee; }
  input, textarea { width: 100%; box-sizing: border-box; padding: 6px; margin: 4px 0; font: inherit; }
  button { padding: 6px 16px; font: inherit; }
</style>
<h1>Sevjala testbed · <?= h($cfg['label']) ?></h1>
<?php if ($error): ?><div class="card bad"><?= h($error) ?></div><?php endif ?>
<?php if (isset($_GET['added'])): ?><div class="card ok">Saved note #<?= (int) $_GET['added'] ?>.</div><?php endif ?>
<?php if ($server): ?>
<div class="card">
  Server <strong><?= h($server['host']) ?></strong> · server_id <?= h($server['id']) ?> · MariaDB <?= h($server['version']) ?>
  · <?= $server['ro'] ? '<span class="warn">read-only</span>' : 'read-write' ?><br>
  <strong><?= number_format($count) ?></strong> notes · <?= number_format($sizeMB) ?> MB in <?= h($cfg['database']) ?>
  <?php if ($replica): ?><br>Replica of <?= h($replica['Master_Host']) ?>:
    IO <?= $replica['Slave_IO_Running'] === 'Yes' ? '<span class="ok">running</span>' : '<span class="warn">' . h($replica['Slave_IO_Running']) . '</span>' ?>,
    SQL <?= $replica['Slave_SQL_Running'] === 'Yes' ? '<span class="ok">running</span>' : '<span class="warn">' . h($replica['Slave_SQL_Running']) . '</span>' ?>,
    behind by <?= $replica['Seconds_Behind_Master'] === null ? '?' : (int) $replica['Seconds_Behind_Master'] . ' s' ?>
    <?php if ($replica['Last_Error'] !== ''): ?><br><span class="warn"><?= h($replica['Last_Error']) ?></span><?php endif ?>
  <?php endif ?>
</div>
<?php endif ?>
<form method="post" class="card">
  <input name="title" placeholder="Title" maxlength="200" required>
  <textarea name="body" rows="2" placeholder="Text"></textarea>
  <button>Add note</button>
</form>
<table>
  <tr><th>#</th><th>Title</th><th>Text</th><th>Created</th></tr>
  <?php foreach ($rows as $row): ?>
  <tr><td><?= (int) $row['id'] ?></td><td><?= h($row['title']) ?></td><td><?= h($row['body']) ?></td><td><?= h($row['created_at']) ?></td></tr>
  <?php endforeach ?>
</table>
<p>
  <?php if ($before): ?><a href="?">Newest</a> · <?php endif ?>
  <?php if (count($rows) === 50): ?><a href="?before=<?= (int) $rows[49]['id'] ?>">Older →</a><?php endif ?>
</p>
