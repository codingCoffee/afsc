<?php
/**
 * Runs WordPress's own content filters for afsc-wp (migration plan §6.2). Reads NDJSON {"id","content"} on stdin
 * and writes NDJSON {"id","html"} (or {"id","error"}) on stdout.
 *
 *   php wp-filter.php [--shortcodes=caption,gallery]            filter mode
 *   php wp-filter.php --render-shortcode=tag [--include=a.php]   render exactly one shortcode with do_shortcode
 *
 * Environment: WP_CORE_DIR (default /usr/src/wordpress), WP_DB_HOST, WP_DB_NAME, WP_DB_USER, WP_DB_PASSWORD,
 * WP_TABLE_PREFIX (default wp_). Use a read-only MySQL user; no plugins or theme are loaded.
 */

$opts = getopt('', ['shortcodes::', 'render-shortcode::', 'include::']);
$core = rtrim(getenv('WP_CORE_DIR') ?: '/usr/src/wordpress', '/') . '/';
if (!is_file($core . 'wp-settings.php')) {
    fwrite(STDERR, "wp-filter: WordPress core not found in $core (set WP_CORE_DIR)\n");
    exit(2);
}

// An empty content dir means no theme functions.php and no plugins are found, whatever the options say.
$empty = sys_get_temp_dir() . '/afsc-wp-empty-content';
@mkdir($empty . '/plugins', 0700, true);
@mkdir($empty . '/mu-plugins', 0700, true);

define('ABSPATH', $core);
define('DB_NAME', getenv('WP_DB_NAME') ?: 'wordpress');
define('DB_USER', getenv('WP_DB_USER') ?: 'wp_readonly');
define('DB_PASSWORD', getenv('WP_DB_PASSWORD') ?: '');
define('DB_HOST', getenv('WP_DB_HOST') ?: '127.0.0.1');
define('DB_CHARSET', 'utf8mb4');
define('DB_COLLATE', '');
define('WP_CONTENT_DIR', $empty);
define('WP_PLUGIN_DIR', $empty . '/plugins');
define('WPMU_PLUGIN_DIR', $empty . '/mu-plugins');
define('DISABLE_WP_CRON', true);
define('WP_DEBUG', false);
define('WP_USE_THEMES', false);
define('WP_INSTALLING', false);
$table_prefix = getenv('WP_TABLE_PREFIX') ?: 'wp_';
if (!preg_match('/^[A-Za-z0-9_]+$/', $table_prefix)) {
    fwrite(STDERR, "wp-filter: invalid WP_TABLE_PREFIX\n");
    exit(2);
}

// WordPress may print notices or DB errors while booting; keep stdout clean for NDJSON.
ob_start();
require ABSPATH . 'wp-settings.php';
$boot = ob_get_clean();
if (trim($boot) !== '') {
    fwrite(STDERR, "wp-filter: output while loading WordPress (ignored):\n" . substr($boot, 0, 2000) . "\n");
}
global $wpdb, $wp_filter, $shortcode_tags;
$wpdb->suppress_errors(true);
$wpdb->show_errors(false);

function afsc_emit($id, $key, $value) {
    echo json_encode(['id' => $id, $key => $value], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
}

$render = isset($opts['render-shortcode']) ? (string) $opts['render-shortcode'] : '';
if ($render !== '') {
    remove_all_shortcodes();
    foreach (array_filter(explode(',', (string) ($opts['include'] ?? ''))) as $file) {
        ob_start();
        require_once $file;
        ob_end_clean();
    }
    foreach (array_keys($shortcode_tags) as $tag) {
        if ($tag !== $render) {
            remove_shortcode($tag);
        }
    }
    if (!shortcode_exists($render)) {
        fwrite(STDERR, "wp-filter: [$render] is not registered by the --include files\n");
        exit(3);
    }
} else {
    remove_all_shortcodes();
    foreach (array_filter(explode(',', (string) ($opts['shortcodes'] ?? ''))) as $tag) {
        add_shortcode($tag, '__return_empty_string');
    }
    // Keep exactly WordPress's core text filters; everything else on the_content is removed.
    $keep = ['wptexturize', 'convert_smilies', 'wpautop', 'shortcode_unautop', 'capital_P_dangit', 'convert_chars'];
    if (isset($wp_filter['the_content'])) {
        foreach ($wp_filter['the_content']->callbacks as $priority => $callbacks) {
            foreach ($callbacks as $cb) {
                $fn = $cb['function'];
                if (!is_string($fn) || !in_array($fn, $keep, true)) {
                    remove_filter('the_content', $fn, $priority);
                }
            }
        }
    }
}

while (($line = fgets(STDIN)) !== false) {
    if (trim($line) === '') {
        continue;
    }
    $item = json_decode($line, true);
    if (!is_array($item) || !isset($item['id'])) {
        fwrite(STDERR, "wp-filter: skipping a line that is not {\"id\",\"content\"}\n");
        continue;
    }
    $id = $item['id'];
    $content = (string) ($item['content'] ?? '');
    try {
        ob_start();
        if ($render !== '') {
            $html = do_shortcode($content);
        } else {
            // do_blocks() would drop wpautop for block content; afsc-wp parses blocks itself, so mirror only that.
            $blocks = has_blocks($content);
            $prio = has_filter('the_content', 'wpautop');
            if ($blocks && $prio !== false) {
                remove_filter('the_content', 'wpautop', $prio);
            }
            $html = apply_filters('the_content', $content);
            if ($blocks && $prio !== false) {
                add_filter('the_content', 'wpautop', $prio);
            }
        }
        $stray = ob_get_clean();
        afsc_emit($id, 'html', $stray . $html);
    } catch (Throwable $e) {
        ob_end_clean();
        afsc_emit($id, 'error', get_class($e) . ': ' . $e->getMessage());
    }
}
