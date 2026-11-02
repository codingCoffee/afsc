<?php
/* Synthetic theme code for afsc-wp tests: one content filter, one head action, menus and an image size. */
require_once __DIR__ . '/inc/shortcodes.php';

add_filter( 'the_content', 'acme_add_disclaimer' );
function acme_add_disclaimer( $content ) {
	return $content . '<p class="disclaimer">Opinions are our own.</p>';
}

add_action( 'wp_head', 'acme_analytics' );
function acme_analytics() {
	echo '<script async src="https://www.googletagmanager.com/gtag/js?id=G-PLACEHOLDER"></script>';
}

register_nav_menus( array( 'header-menu' => 'Header', 'footer-menu' => 'Footer' ) );
add_image_size( 'card', 600, 400, true );
