<?php
// Settings come from the environment (Docker) or from config.local.php (XAMPP),
// which sets $cfg['password'] and anything else that differs.
$cfg = array(
    'host' => getenv('DB_HOST') ? getenv('DB_HOST') : '127.0.0.1',
    'port' => getenv('DB_PORT') ? (int) getenv('DB_PORT') : 3306,
    'user' => getenv('DB_USER') ? getenv('DB_USER') : 'app',
    'password' => getenv('DB_PASSWORD') ? getenv('DB_PASSWORD') : '',
    'database' => getenv('DB_NAME') ? getenv('DB_NAME') : 'sevtest',
    'label' => getenv('APP_LABEL') ? getenv('APP_LABEL') : 'Windows (primary)',
);
if (is_file(__DIR__ . '/config.local.php')) {
    include __DIR__ . '/config.local.php';
}
