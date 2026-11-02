<?php
/* Registers [pullquote]; afsc-wp renders it through tools/wp-filter.php --render-shortcode=pullquote. */
add_shortcode( 'pullquote', function ( $atts, $content = '' ) {
	return '<blockquote class="pullquote"><p>' . esc_html( $content ) . '</p></blockquote>';
} );
