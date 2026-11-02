-- Synthetic WordPress fixture for afsc-wp tests. Schema follows WordPress 6.9.0 (wp-admin/includes/schema.php).
-- All data is placeholder data. db_version below is a placeholder; WordPress only checks it in wp-admin.
-- Load with: mysql --default-character-set=utf8mb4 wordpress < wp-fixture.sql
SET NAMES utf8mb4;
SET sql_mode = 'NO_ENGINE_SUBSTITUTION';
DROP TABLE IF EXISTS `wp_users`;
DROP TABLE IF EXISTS `wp_usermeta`;
DROP TABLE IF EXISTS `wp_posts`;
DROP TABLE IF EXISTS `wp_postmeta`;
DROP TABLE IF EXISTS `wp_comments`;
DROP TABLE IF EXISTS `wp_commentmeta`;
DROP TABLE IF EXISTS `wp_terms`;
DROP TABLE IF EXISTS `wp_term_taxonomy`;
DROP TABLE IF EXISTS `wp_term_relationships`;
DROP TABLE IF EXISTS `wp_termmeta`;
DROP TABLE IF EXISTS `wp_options`;
DROP TABLE IF EXISTS `wp_links`;
CREATE TABLE `wp_users` (
  ID bigint(20) unsigned NOT NULL auto_increment, user_login varchar(60) NOT NULL default '', user_pass varchar(255) NOT NULL default '',
  user_nicename varchar(50) NOT NULL default '', user_email varchar(100) NOT NULL default '', user_url varchar(100) NOT NULL default '',
  user_registered datetime NOT NULL default '0000-00-00 00:00:00', user_activation_key varchar(255) NOT NULL default '',
  user_status int(11) NOT NULL default '0', display_name varchar(250) NOT NULL default '',
  PRIMARY KEY (ID), KEY user_login_key (user_login), KEY user_nicename (user_nicename), KEY user_email (user_email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_usermeta` (
  umeta_id bigint(20) unsigned NOT NULL auto_increment, user_id bigint(20) unsigned NOT NULL default '0',
  meta_key varchar(255) default NULL, meta_value longtext,
  PRIMARY KEY (umeta_id), KEY user_id (user_id), KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_posts` (
  ID bigint(20) unsigned NOT NULL auto_increment, post_author bigint(20) unsigned NOT NULL default '0',
  post_date datetime NOT NULL default '0000-00-00 00:00:00', post_date_gmt datetime NOT NULL default '0000-00-00 00:00:00',
  post_content longtext NOT NULL, post_title text NOT NULL, post_excerpt text NOT NULL,
  post_status varchar(20) NOT NULL default 'publish', comment_status varchar(20) NOT NULL default 'open',
  ping_status varchar(20) NOT NULL default 'open', post_password varchar(255) NOT NULL default '',
  post_name varchar(200) NOT NULL default '', to_ping text NOT NULL, pinged text NOT NULL,
  post_modified datetime NOT NULL default '0000-00-00 00:00:00', post_modified_gmt datetime NOT NULL default '0000-00-00 00:00:00',
  post_content_filtered longtext NOT NULL, post_parent bigint(20) unsigned NOT NULL default '0', guid varchar(255) NOT NULL default '',
  menu_order int(11) NOT NULL default '0', post_type varchar(20) NOT NULL default 'post', post_mime_type varchar(100) NOT NULL default '',
  comment_count bigint(20) NOT NULL default '0',
  PRIMARY KEY (ID), KEY post_name (post_name(191)), KEY type_status_date (post_type,post_status,post_date,ID),
  KEY post_parent (post_parent), KEY post_author (post_author)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_postmeta` (
  meta_id bigint(20) unsigned NOT NULL auto_increment, post_id bigint(20) unsigned NOT NULL default '0',
  meta_key varchar(255) default NULL, meta_value longtext,
  PRIMARY KEY (meta_id), KEY post_id (post_id), KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
-- latin1 on purpose: the gotcha list's double-encoded UTF-8 (comment 107).
CREATE TABLE `wp_comments` (
  comment_ID bigint(20) unsigned NOT NULL auto_increment, comment_post_ID bigint(20) unsigned NOT NULL default '0',
  comment_author tinytext NOT NULL, comment_author_email varchar(100) NOT NULL default '', comment_author_url varchar(200) NOT NULL default '',
  comment_author_IP varchar(100) NOT NULL default '', comment_date datetime NOT NULL default '0000-00-00 00:00:00',
  comment_date_gmt datetime NOT NULL default '0000-00-00 00:00:00', comment_content text NOT NULL, comment_karma int(11) NOT NULL default '0',
  comment_approved varchar(20) NOT NULL default '1', comment_agent varchar(255) NOT NULL default '', comment_type varchar(20) NOT NULL default 'comment',
  comment_parent bigint(20) unsigned NOT NULL default '0', user_id bigint(20) unsigned NOT NULL default '0',
  PRIMARY KEY (comment_ID), KEY comment_post_ID (comment_post_ID), KEY comment_parent (comment_parent)
) ENGINE=InnoDB DEFAULT CHARSET=latin1;
CREATE TABLE `wp_commentmeta` (
  meta_id bigint(20) unsigned NOT NULL auto_increment, comment_id bigint(20) unsigned NOT NULL default '0',
  meta_key varchar(255) default NULL, meta_value longtext, PRIMARY KEY (meta_id), KEY comment_id (comment_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_terms` (
  term_id bigint(20) unsigned NOT NULL auto_increment, name varchar(200) NOT NULL default '', slug varchar(200) NOT NULL default '',
  term_group bigint(10) NOT NULL default 0, PRIMARY KEY (term_id), KEY slug (slug(191)), KEY name (name(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_term_taxonomy` (
  term_taxonomy_id bigint(20) unsigned NOT NULL auto_increment, term_id bigint(20) unsigned NOT NULL default 0,
  taxonomy varchar(32) NOT NULL default '', description longtext NOT NULL, parent bigint(20) unsigned NOT NULL default 0,
  count bigint(20) NOT NULL default 0, PRIMARY KEY (term_taxonomy_id), UNIQUE KEY term_id_taxonomy (term_id,taxonomy), KEY taxonomy (taxonomy)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_term_relationships` (
  object_id bigint(20) unsigned NOT NULL default 0, term_taxonomy_id bigint(20) unsigned NOT NULL default 0,
  term_order int(11) NOT NULL default 0, PRIMARY KEY (object_id,term_taxonomy_id), KEY term_taxonomy_id (term_taxonomy_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_termmeta` (
  meta_id bigint(20) unsigned NOT NULL auto_increment, term_id bigint(20) unsigned NOT NULL default '0',
  meta_key varchar(255) default NULL, meta_value longtext, PRIMARY KEY (meta_id), KEY term_id (term_id), KEY meta_key (meta_key(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_options` (
  option_id bigint(20) unsigned NOT NULL auto_increment, option_name varchar(191) NOT NULL default '', option_value longtext NOT NULL,
  autoload varchar(20) NOT NULL default 'yes', PRIMARY KEY (option_id), UNIQUE KEY option_name (option_name), KEY autoload (autoload)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
CREATE TABLE `wp_links` (
  link_id bigint(20) unsigned NOT NULL auto_increment, link_url varchar(255) NOT NULL default '', link_name varchar(255) NOT NULL default '',
  PRIMARY KEY (link_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;
INSERT INTO `wp_users` (ID,user_login,user_pass,user_nicename,user_email,user_url,user_registered,display_name) VALUES (1,'admin','$P$placeholder-hash-not-real','admin-a','admin.fx@example.com','https://example.com','2021-12-01 00:00:00','Admin A');
INSERT INTO `wp_users` (ID,user_login,user_pass,user_nicename,user_email,user_url,user_registered,display_name) VALUES (2,'editor','$P$placeholder-hash-not-real','editor-b','editor.fx@example.com','','2021-12-01 00:00:00','Editor B');
INSERT INTO `wp_users` (ID,user_login,user_pass,user_nicename,user_email,user_url,user_registered,display_name) VALUES (3,'authorc','$P$placeholder-hash-not-real','author-c','writer.fx@example.com','https://example.org/author-c','2021-12-01 00:00:00','Author C');
INSERT INTO `wp_users` (ID,user_login,user_pass,user_nicename,user_email,user_url,user_registered,display_name) VALUES (4,'subscriber','$P$placeholder-hash-not-real','subscriber-d','sub.fx@example.com','','2021-12-01 00:00:00','Subscriber D');
INSERT INTO `wp_users` (ID,user_login,user_pass,user_nicename,user_email,user_url,user_registered,display_name) VALUES (5,'contrib','$P$placeholder-hash-not-real','contrib-e','contrib.fx@example.com','','2021-12-01 00:00:00','Contributor E');
INSERT INTO `wp_usermeta` VALUES (1,1,'wp_capabilities','a:1:{s:13:"administrator";b:1;}');
INSERT INTO `wp_usermeta` VALUES (2,1,'nickname','admin');
INSERT INTO `wp_usermeta` VALUES (3,1,'description','Bio of Admin A.');
INSERT INTO `wp_usermeta` VALUES (4,1,'session_tokens','a:1:{s:17:"placeholder-token";a:1:{s:10:"expiration";i:1;}}');
INSERT INTO `wp_usermeta` VALUES (5,2,'wp_capabilities','a:1:{s:6:"editor";b:1;}');
INSERT INTO `wp_usermeta` VALUES (6,2,'nickname','editor');
INSERT INTO `wp_usermeta` VALUES (7,2,'description','Bio of Editor B.');
INSERT INTO `wp_usermeta` VALUES (8,2,'session_tokens','a:1:{s:17:"placeholder-token";a:1:{s:10:"expiration";i:1;}}');
INSERT INTO `wp_usermeta` VALUES (9,3,'wp_capabilities','a:1:{s:6:"author";b:1;}');
INSERT INTO `wp_usermeta` VALUES (10,3,'nickname','authorc');
INSERT INTO `wp_usermeta` VALUES (11,3,'description','Bio of Author C.');
INSERT INTO `wp_usermeta` VALUES (12,3,'session_tokens','a:1:{s:17:"placeholder-token";a:1:{s:10:"expiration";i:1;}}');
INSERT INTO `wp_usermeta` VALUES (13,4,'wp_capabilities','a:1:{s:10:"subscriber";b:1;}');
INSERT INTO `wp_usermeta` VALUES (14,4,'nickname','subscriber');
INSERT INTO `wp_usermeta` VALUES (15,4,'description','Bio of Subscriber D.');
INSERT INTO `wp_usermeta` VALUES (16,4,'session_tokens','a:1:{s:17:"placeholder-token";a:1:{s:10:"expiration";i:1;}}');
INSERT INTO `wp_usermeta` VALUES (17,5,'wp_capabilities','a:1:{s:11:"contributor";b:1;}');
INSERT INTO `wp_usermeta` VALUES (18,5,'nickname','contrib');
INSERT INTO `wp_usermeta` VALUES (19,5,'description','Bio of Contributor E.');
INSERT INTO `wp_usermeta` VALUES (20,5,'session_tokens','a:1:{s:17:"placeholder-token";a:1:{s:10:"expiration";i:1;}}');
INSERT INTO `wp_usermeta` VALUES (21,2,'metronet_image_id','40');
INSERT INTO `wp_usermeta` VALUES (22,3,'twitter','authorc');
INSERT INTO `wp_usermeta` VALUES (23,3,'facebook','https://facebook.com/author-c');
INSERT INTO `wp_usermeta` VALUES (24,3,'wpseo_title','%%name%% writes here %%sep%% %%sitename%%');
INSERT INTO `wp_usermeta` VALUES (25,3,'wpseo_metadesc','Posts by Author C.');
INSERT INTO `wp_usermeta` VALUES (26,1,'community-events-location','a:1:{s:2:"ip";s:9:"192.0.2.0";}');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (10,1,'2022-03-01 10:30:00','2022-03-01 05:00:00','Welcome to "Example Blog" -- a place for notes... It\'s simple.\n\n[caption id="attachment_30" align="alignright" width="300"]<a href="https://blog.example.com/wp-content/uploads/2022/03/big-chart.png"><img class="size-medium wp-image-30" src="https://blog.example.com/wp-content/uploads/2022/03/big-chart-30x17.png" alt="Bar chart" width="300" height="169" /></a> Monthly <em>numbers</em>[/caption]\n\nRead the <a href="https://blog.example.com/?p=12">café piece</a> and the <a href="http://old.example.com/company/history/">history page</a>.\n<!--more-->\nHere is the original via Jetpack: <img src="https://i0.wp.com/blog.example.com/wp-content/uploads/2022/03/big-chart.png?resize=64%2C36&ssl=1" alt="" />\n\nSee <a href="https://blog.example.com/?attachment_id=30">the chart page</a>.','Hello “World”','A short <b>excerpt</b> &amp; more.','publish','open','','hello-world','','','2022-03-01 10:30:00','2022-03-01 05:00:00','',0,'http://blog.example.com/?p=10',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (11,3,'2022-03-05 09:00:00','2022-03-05 03:30:00','[gallery columns="2" link="file"]\n\n<table>\n<thead><tr><th>Item</th><th>Value</th></tr></thead>\n<tbody><tr><td>Apples</td><td>3</td></tr></tbody>\n</table>\n<ul>\n	<li>First</li>\n	<li>Second\n<ol>\n	<li>Nested</li>\n</ol>\n</li>\n</ul>\n<pre class="language-go">fmt.Println("hi")</pre>\n[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]\n\nhttps://vimeo.com/76979871\n\nBare line done.','Gallery post','','publish','closed','','gallery-post','','','2022-03-05 09:00:00','2022-03-05 03:30:00','',0,'http://blog.example.com/?p=11',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (12,2,'2022-03-10 12:00:00','2022-03-10 06:30:00','<!-- wp:paragraph -->\n<p>A "café" story in blocks.</p>\n<!-- /wp:paragraph -->\n\n<!-- wp:heading -->\n<h2 class="wp-block-heading" id="the-menu">The menu</h2>\n<!-- /wp:heading -->\n\n<!-- wp:image {"id":30,"sizeSlug":"large","linkDestination":"none"} -->\n<figure class="wp-block-image size-large"><img src="https://blog.example.com/wp-content/uploads/2022/03/big-chart-scaled.png" alt="" class="wp-image-30"/><figcaption class="wp-element-caption">Prices this year</figcaption></figure>\n<!-- /wp:image -->\n\n<!-- wp:embed {"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","type":"video","providerNameSlug":"youtube"} -->\n<figure class="wp-block-embed is-type-video is-provider-youtube wp-block-embed-youtube"><div class="wp-block-embed__wrapper">\nhttps://www.youtube.com/watch?v=dQw4w9WgXcQ\n</div></figure>\n<!-- /wp:embed -->\n\n<!-- wp:html -->\n<div class="note">Opening hours vary.</div><script>console.log("x")</script>\n<!-- /wp:html -->\n\n<!-- wp:acme/opening-hours {"days":5} /-->','Café culture','','publish','open','','caf%c3%a9-culture','','','2022-03-10 12:00:00','2022-03-10 06:30:00','',0,'http://blog.example.com/?p=12',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (13,1,'2022-02-15 08:00:00','2022-02-15 02:30:00','Page one text.\n\n<p>&nbsp;</p>\n\n<div class="callout">A boxed note with <span style="color: #cc0000">red text</span>.</div>\n\n[pullquote]Good writing is rewriting[/pullquote]\n\nWe say [sic] here, and cite [1].\n<!--nextpage-->\nPage two has an external image: <img src="https://cdn.other.example.org/pic.png" alt="External" width="40" height="20" />\n\nAnd a missing one: <img src="https://blog.example.com/wp-content/uploads/2022/09/gone.png" alt="Gone" />\n<!--nextpage-->\n<iframe src="https://forms.example.org/embed/1" width="400" height="300"></iframe>\n\nPage three ends here.','Long read','','publish','open','','long-read','','','2022-02-15 08:00:00','2022-02-15 02:30:00','',0,'http://blog.example.com/?p=13',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (14,2,'2030-01-01 09:00:00','2030-01-01 03:30:00','Scheduled text.','Coming soon','','future','open','','coming-soon','','','2030-01-01 09:00:00','2030-01-01 03:30:00','',0,'http://blog.example.com/?p=14',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (15,3,'2022-06-01 10:00:00','0000-00-00 00:00:00','Draft text.','Untitled idea','','draft','open','','','','','2022-06-01 10:00:00','0000-00-00 00:00:00','',0,'http://blog.example.com/?p=15',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (16,1,'2022-04-01 10:00:00','2022-04-01 04:30:00','Only for admins.','Private notes','','private','open','','private-notes','','','2022-04-01 10:00:00','2022-04-01 04:30:00','',0,'http://blog.example.com/?p=16',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (17,2,'2022-04-02 10:00:00','2022-04-02 04:30:00','Protected text.','Members only','','publish','open','placeholder-pass','members-only','','','2022-04-02 10:00:00','2022-04-02 04:30:00','',0,'http://blog.example.com/?p=17',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (18,1,'2021-01-01 10:00:00','2021-01-01 04:30:00','Gone.','Old trash','','trash','open','','old-trash__trashed','','','2021-01-01 10:00:00','2021-01-01 04:30:00','',0,'http://blog.example.com/?p=18',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (19,1,'2022-03-01 10:00:00','2022-03-01 04:30:00','Old draft of post 10.','Hello “World”','','inherit','closed','','10-revision-v1','','','2022-03-01 10:00:00','2022-03-01 04:30:00','',10,'http://blog.example.com/?p=19',0,'revision','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (20,1,'2022-01-01 10:00:00','2022-01-01 04:30:00','Welcome home. It\'s the front page.','Home','','publish','closed','','home-fx','','','2022-01-01 10:00:00','2022-01-01 04:30:00','',0,'http://blog.example.com/?p=20',0,'page','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (21,1,'2022-01-01 10:05:00','2022-01-01 04:35:00','Latest posts appear here.','Journal','','publish','closed','','journal','','','2022-01-01 10:05:00','2022-01-01 04:35:00','',0,'http://blog.example.com/?p=21',1,'page','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (22,1,'2022-01-02 10:00:00','2022-01-02 04:30:00','About the company.','Company','','publish','closed','','company','','','2022-01-02 10:00:00','2022-01-02 04:30:00','',0,'http://blog.example.com/?p=22',2,'page','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (23,1,'2022-01-03 10:00:00','2022-01-03 04:30:00','Founded long ago.','History','','publish','closed','','history','','','2022-01-03 10:00:00','2022-01-03 04:30:00','',22,'http://blog.example.com/?p=23',0,'page','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (24,1,'2022-07-01 10:00:00','0000-00-00 00:00:00','','Auto Draft','','auto-draft','open','','','','','2022-07-01 10:00:00','0000-00-00 00:00:00','',0,'http://blog.example.com/?p=24',0,'post','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (25,1,'2022-05-01 10:00:00','2022-05-01 04:30:00','An event post type.','Launch party','','publish','closed','','launch-party','','','2022-05-01 10:00:00','2022-05-01 04:30:00','',0,'http://blog.example.com/?p=25',0,'event','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (30,1,'2022-03-01 10:00:00','2022-03-01 04:30:00','Chart description','Big chart','Monthly numbers','inherit','open','','big-chart','','','2022-03-01 10:00:00','2022-03-01 04:30:00','',10,'https://blog.example.com/wp-content/uploads/2022/03/big-chart.png',0,'attachment','image/png');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (31,3,'2022-03-05 08:00:00','2022-03-05 02:30:00','','Photo A','','inherit','open','','photo-a','','','2022-03-05 08:00:00','2022-03-05 02:30:00','',11,'https://blog.example.com/wp-content/uploads/2022/03/photo-a.png',1,'attachment','image/png');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (32,3,'2022-03-05 08:01:00','2022-03-05 02:31:00','','Photo B','','inherit','open','','photo-b','','','2022-03-05 08:01:00','2022-03-05 02:31:00','',11,'https://blog.example.com/wp-content/uploads/2022/03/photo-b.png',0,'attachment','image/png');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (33,3,'2022-04-05 08:00:00','2022-04-05 02:30:00','','Photo A copy','','inherit','open','','photo-a-copy','','','2022-04-05 08:00:00','2022-04-05 02:30:00','',0,'https://blog.example.com/wp-content/uploads/2022/03/photo-a-copy.png',0,'attachment','image/png');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (34,1,'2022-09-01 08:00:00','2022-09-01 02:30:00','','Gone','','inherit','open','','gone','','','2022-09-01 08:00:00','2022-09-01 02:30:00','',13,'https://blog.example.com/wp-content/uploads/2022/03/gone.png',0,'attachment','image/png');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (40,2,'2022-01-01 08:00:00','2022-01-01 02:30:00','','Avatar B','','inherit','open','','avatar-b','','','2022-01-01 08:00:00','2022-01-01 02:30:00','',0,'https://blog.example.com/wp-content/uploads/2022/03/avatar-b.png',0,'attachment','image/png');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (50,1,'2022-01-05 10:00:00','2022-01-05 04:30:00','','','','publish','closed','','50','','','2022-01-05 10:00:00','2022-01-05 04:30:00','',0,'http://blog.example.com/?p=50',1,'nav_menu_item','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (51,1,'2022-01-05 10:00:00','2022-01-05 04:30:00','','Economy','','publish','closed','','51','','','2022-01-05 10:00:00','2022-01-05 04:30:00','',0,'http://blog.example.com/?p=51',2,'nav_menu_item','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (52,1,'2022-01-05 10:00:00','2022-01-05 04:30:00','','Journal','','publish','closed','','52','','','2022-01-05 10:00:00','2022-01-05 04:30:00','',0,'http://blog.example.com/?p=52',3,'nav_menu_item','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (53,1,'2022-01-05 10:00:00','2022-01-05 04:30:00','','Hello post','','publish','closed','','53','','','2022-01-05 10:00:00','2022-01-05 04:30:00','',0,'http://blog.example.com/?p=53',4,'nav_menu_item','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (54,1,'2022-01-05 10:00:00','2022-01-05 04:30:00','','Example.org','','publish','closed','','54','','','2022-01-05 10:00:00','2022-01-05 04:30:00','',0,'http://blog.example.com/?p=54',1,'nav_menu_item','');
INSERT INTO `wp_posts` (ID,post_author,post_date,post_date_gmt,post_content,post_title,post_excerpt,post_status,comment_status,post_password,post_name,to_ping,pinged,post_modified,post_modified_gmt,post_content_filtered,post_parent,guid,menu_order,post_type,post_mime_type) VALUES (60,1,'2022-01-05 10:00:00','2022-01-05 04:30:00','<!-- wp:paragraph --><p>Reusable</p><!-- /wp:paragraph -->','Reusable','','publish','closed','','reusable','','','2022-01-05 10:00:00','2022-01-05 04:30:00','',0,'http://blog.example.com/?p=60',0,'wp_block','');
INSERT INTO `wp_postmeta` VALUES (1,10,'_thumbnail_id','30');
INSERT INTO `wp_postmeta` VALUES (2,10,'_yoast_wpseo_title','%%title%% %%sep%% %%sitename%% %%focuskw%%');
INSERT INTO `wp_postmeta` VALUES (3,10,'_yoast_wpseo_metadesc','A post about “quotes”.');
INSERT INTO `wp_postmeta` VALUES (4,10,'_yoast_wpseo_focuskw','quotes');
INSERT INTO `wp_postmeta` VALUES (5,10,'_yoast_wpseo_meta-robots-noindex','2');
INSERT INTO `wp_postmeta` VALUES (6,10,'_yoast_wpseo_primary_category','3');
INSERT INTO `wp_postmeta` VALUES (7,10,'_yoast_wpseo_is_cornerstone','1');
INSERT INTO `wp_postmeta` VALUES (8,10,'_yoast_wpseo_opengraph-image-id','30');
INSERT INTO `wp_postmeta` VALUES (9,10,'_yoast_wpseo_opengraph-image','https://blog.example.com/wp-content/uploads/2022/03/big-chart-scaled.png');
INSERT INTO `wp_postmeta` VALUES (10,10,'_wp_old_slug','hello-world-old');
INSERT INTO `wp_postmeta` VALUES (11,10,'_edit_lock','1646100000:1');
INSERT INTO `wp_postmeta` VALUES (12,11,'_yoast_wpseo_video_meta','a:8:{s:3:"url";s:43:"https://www.youtube.com/watch?v=dQw4w9WgXcQ";s:13:"thumbnail_loc";s:48:"https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg";s:5:"title";s:13:"Example video";s:11:"description";s:24:"A synthetic video entry.";s:8:"duration";i:212;s:16:"publication_date";s:25:"2022-02-01T00:00:00+00:00";s:4:"type";s:7:"youtube";s:2:"id";s:11:"dQw4w9WgXcQ";}');
INSERT INTO `wp_postmeta` VALUES (13,11,'guest_author_name','Guest Writer');
INSERT INTO `wp_postmeta` VALUES (14,11,'guest_author_email','guest@example.com');
INSERT INTO `wp_postmeta` VALUES (15,11,'guest_author_url','https://example.org/guest');
INSERT INTO `wp_postmeta` VALUES (16,11,'_oembed_5f1c1c0e2e7c7d8a9b0c1d2e3f405162','<iframe title="Example" src="https://player.vimeo.com/video/76979871?dnt=1&amp;app_id=1" width="640" height="360"></iframe>');
INSERT INTO `wp_postmeta` VALUES (17,11,'_oembed_time_5f1c1c0e2e7c7d8a9b0c1d2e3f405162','1646450000');
INSERT INTO `wp_postmeta` VALUES (18,11,'_yoast_wpseo_meta-robots-noindex','1');
INSERT INTO `wp_postmeta` VALUES (19,12,'_yoast_wpseo_canonical','https://blog.example.com/2022/03/caf%c3%a9-culture/');
INSERT INTO `wp_postmeta` VALUES (20,13,'_wp_old_date','2022-01-01');
INSERT INTO `wp_postmeta` VALUES (21,13,'_yoast_wpseo_canonical','https://other.example.org/long-read');
INSERT INTO `wp_postmeta` VALUES (22,22,'_wp_page_template','templates/full-width.php');
INSERT INTO `wp_postmeta` VALUES (23,30,'_wp_attached_file','2022/03/big-chart-scaled.png');
INSERT INTO `wp_postmeta` VALUES (24,30,'_wp_attachment_metadata','a:7:{s:5:"width";i:48;s:6:"height";i:27;s:4:"file";s:28:"2022/03/big-chart-scaled.png";s:8:"filesize";i:120;s:5:"sizes";a:2:{s:6:"medium";a:4:{s:4:"file";s:19:"big-chart-30x17.png";s:5:"width";i:30;s:6:"height";i:17;s:9:"mime-type";s:9:"image/png";}s:9:"thumbnail";a:4:{s:4:"file";s:19:"big-chart-15x15.png";s:5:"width";i:15;s:6:"height";i:15;s:9:"mime-type";s:9:"image/png";}}s:10:"image_meta";a:3:{s:7:"caption";s:0:"";s:5:"title";s:0:"";s:9:"copyright";s:0:"";}s:14:"original_image";s:13:"big-chart.png";}');
INSERT INTO `wp_postmeta` VALUES (25,30,'_wp_attachment_image_alt','Bar chart of example data');
INSERT INTO `wp_postmeta` VALUES (26,31,'_wp_attached_file','2022/03/photo-a.png');
INSERT INTO `wp_postmeta` VALUES (27,31,'_wp_attachment_metadata','a:4:{s:5:"width";i:20;s:6:"height";i:20;s:4:"file";s:19:"2022/03/photo-a.png";s:5:"sizes";a:0:{}}');
INSERT INTO `wp_postmeta` VALUES (28,32,'_wp_attached_file','2022/03/photo-b.png');
INSERT INTO `wp_postmeta` VALUES (29,32,'_wp_attachment_metadata','a:4:{s:5:"width";i:20;s:6:"height";i:20;s:4:"file";s:19:"2022/03/photo-b.png";s:5:"sizes";a:0:{}}');
INSERT INTO `wp_postmeta` VALUES (30,33,'_wp_attached_file','2022/04/photo-a-copy.png');
INSERT INTO `wp_postmeta` VALUES (31,34,'_wp_attached_file','2022/09/gone.png');
INSERT INTO `wp_postmeta` VALUES (32,40,'_wp_attached_file','2022/01/avatar-b.png');
INSERT INTO `wp_postmeta` VALUES (33,50,'_menu_item_type','post_type');
INSERT INTO `wp_postmeta` VALUES (34,50,'_menu_item_object','page');
INSERT INTO `wp_postmeta` VALUES (35,50,'_menu_item_object_id','20');
INSERT INTO `wp_postmeta` VALUES (36,50,'_menu_item_url','');
INSERT INTO `wp_postmeta` VALUES (37,50,'_menu_item_menu_item_parent','0');
INSERT INTO `wp_postmeta` VALUES (38,50,'_menu_item_target','');
INSERT INTO `wp_postmeta` VALUES (39,50,'_menu_item_classes','a:1:{i:0;s:0:"";}');
INSERT INTO `wp_postmeta` VALUES (40,50,'_menu_item_xfn','');
INSERT INTO `wp_postmeta` VALUES (41,51,'_menu_item_type','taxonomy');
INSERT INTO `wp_postmeta` VALUES (42,51,'_menu_item_object','category');
INSERT INTO `wp_postmeta` VALUES (43,51,'_menu_item_object_id','2');
INSERT INTO `wp_postmeta` VALUES (44,51,'_menu_item_url','');
INSERT INTO `wp_postmeta` VALUES (45,51,'_menu_item_menu_item_parent','0');
INSERT INTO `wp_postmeta` VALUES (46,51,'_menu_item_target','');
INSERT INTO `wp_postmeta` VALUES (47,51,'_menu_item_classes','a:1:{i:0;s:12:"menu-economy";}');
INSERT INTO `wp_postmeta` VALUES (48,51,'_menu_item_xfn','');
INSERT INTO `wp_postmeta` VALUES (49,52,'_menu_item_type','custom');
INSERT INTO `wp_postmeta` VALUES (50,52,'_menu_item_object','custom');
INSERT INTO `wp_postmeta` VALUES (51,52,'_menu_item_object_id','52');
INSERT INTO `wp_postmeta` VALUES (52,52,'_menu_item_url','https://blog.example.com/journal/');
INSERT INTO `wp_postmeta` VALUES (53,52,'_menu_item_menu_item_parent','0');
INSERT INTO `wp_postmeta` VALUES (54,52,'_menu_item_target','');
INSERT INTO `wp_postmeta` VALUES (55,52,'_menu_item_classes','a:1:{i:0;s:0:"";}');
INSERT INTO `wp_postmeta` VALUES (56,52,'_menu_item_xfn','');
INSERT INTO `wp_postmeta` VALUES (57,53,'_menu_item_type','post_type');
INSERT INTO `wp_postmeta` VALUES (58,53,'_menu_item_object','post');
INSERT INTO `wp_postmeta` VALUES (59,53,'_menu_item_object_id','10');
INSERT INTO `wp_postmeta` VALUES (60,53,'_menu_item_url','');
INSERT INTO `wp_postmeta` VALUES (61,53,'_menu_item_menu_item_parent','52');
INSERT INTO `wp_postmeta` VALUES (62,53,'_menu_item_target','');
INSERT INTO `wp_postmeta` VALUES (63,53,'_menu_item_classes','a:1:{i:0;s:0:"";}');
INSERT INTO `wp_postmeta` VALUES (64,53,'_menu_item_xfn','');
INSERT INTO `wp_postmeta` VALUES (65,54,'_menu_item_type','custom');
INSERT INTO `wp_postmeta` VALUES (66,54,'_menu_item_object','custom');
INSERT INTO `wp_postmeta` VALUES (67,54,'_menu_item_object_id','54');
INSERT INTO `wp_postmeta` VALUES (68,54,'_menu_item_url','https://example.org/');
INSERT INTO `wp_postmeta` VALUES (69,54,'_menu_item_menu_item_parent','0');
INSERT INTO `wp_postmeta` VALUES (70,54,'_menu_item_target','_blank');
INSERT INTO `wp_postmeta` VALUES (71,54,'_menu_item_classes','a:2:{i:0;s:8:"external";i:1;s:10:"bad class!";}');
INSERT INTO `wp_postmeta` VALUES (72,54,'_menu_item_xfn','');
INSERT INTO `wp_terms` VALUES (1,'Uncategorized','uncategorized',0);
INSERT INTO `wp_term_taxonomy` VALUES (1,1,'category','',0,0);
INSERT INTO `wp_terms` VALUES (2,'Economy','economy',0);
INSERT INTO `wp_term_taxonomy` VALUES (2,2,'category','Notes on the economy.',0,1);
INSERT INTO `wp_terms` VALUES (3,'Local News','local-news',0);
INSERT INTO `wp_term_taxonomy` VALUES (3,3,'category','',2,2);
INSERT INTO `wp_terms` VALUES (4,'Café Culture','caf%c3%a9-culture',0);
INSERT INTO `wp_term_taxonomy` VALUES (4,4,'category','',0,1);
INSERT INTO `wp_terms` VALUES (5,'Charts','charts',0);
INSERT INTO `wp_term_taxonomy` VALUES (5,5,'post_tag','',0,1);
INSERT INTO `wp_terms` VALUES (6,'नमस','%e0%a4%a8%e0%a4%ae%e0%a4%b8',0);
INSERT INTO `wp_term_taxonomy` VALUES (6,6,'post_tag','',0,1);
INSERT INTO `wp_terms` VALUES (7,'Main Menu','main-menu',0);
INSERT INTO `wp_term_taxonomy` VALUES (7,7,'nav_menu','',0,4);
INSERT INTO `wp_terms` VALUES (8,'Footer Links','footer-links',0);
INSERT INTO `wp_term_taxonomy` VALUES (8,8,'nav_menu','',0,1);
INSERT INTO `wp_terms` VALUES (9,'post-format-gallery','post-format-gallery',0);
INSERT INTO `wp_term_taxonomy` VALUES (9,9,'post_format','',0,1);
INSERT INTO `wp_term_relationships` VALUES (10,2,0);
INSERT INTO `wp_term_relationships` VALUES (10,3,0);
INSERT INTO `wp_term_relationships` VALUES (10,5,0);
INSERT INTO `wp_term_relationships` VALUES (11,3,0);
INSERT INTO `wp_term_relationships` VALUES (11,9,0);
INSERT INTO `wp_term_relationships` VALUES (12,4,0);
INSERT INTO `wp_term_relationships` VALUES (12,6,0);
INSERT INTO `wp_term_relationships` VALUES (13,1,0);
INSERT INTO `wp_term_relationships` VALUES (14,2,0);
INSERT INTO `wp_term_relationships` VALUES (15,1,0);
INSERT INTO `wp_term_relationships` VALUES (16,1,0);
INSERT INTO `wp_term_relationships` VALUES (17,1,0);
INSERT INTO `wp_term_relationships` VALUES (50,7,0);
INSERT INTO `wp_term_relationships` VALUES (51,7,0);
INSERT INTO `wp_term_relationships` VALUES (52,7,0);
INSERT INTO `wp_term_relationships` VALUES (53,7,0);
INSERT INTO `wp_term_relationships` VALUES (54,8,0);
INSERT INTO `wp_termmeta` VALUES (1,2,'color','#336699');
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (100,10,'Commenter A','commenter@example.com','https://example.org','192.0.2.10','2022-03-02 10:00:00','2022-03-02 04:30:00','Nice post! See <a href="https://example.org/more">my notes</a> and https://example.org.\n\nSecond paragraph.','1','Mozilla/5.0 (placeholder)','comment',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (101,10,'Editor B','editor.fx@example.com','','192.0.2.11','2022-03-02 11:00:00','2022-03-02 05:30:00','Thanks!','1','Mozilla/5.0 (placeholder)','comment',100,2);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (102,10,'Commenter C','commenter.c@example.com','javascript:alert(1)','2001:db8::1','2022-03-03 10:00:00','2022-03-03 04:30:00','Pending question?','0','Mozilla/5.0 (placeholder)','',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (103,10,'Spammer','spam@example.com','https://spam.example.com','198.51.100.7','2022-03-03 11:00:00','2022-03-03 05:30:00','Buy now','spam','Mozilla/5.0 (placeholder)','comment',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (104,10,'Other blog','','https://other.example.org/post','198.51.100.8','2022-03-04 10:00:00','2022-03-04 04:30:00','Pingback text','1','Mozilla/5.0 (placeholder)','pingback',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (105,11,'Commenter D','commenter.d@example.com','','192.0.2.12','2022-03-06 10:00:00','2022-03-06 04:30:00','Trashed remark','trash','Mozilla/5.0 (placeholder)','comment',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (106,18,'Commenter E','commenter.e@example.com','','192.0.2.13','2021-01-02 10:00:00','2021-01-02 04:30:00','On a trashed post','1','Mozilla/5.0 (placeholder)','comment',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (107,12,'Commenter F','commenter.f@example.com','','192.0.2.14','2022-03-11 10:00:00','2022-03-11 04:30:00','Itâ€™s great','1','Mozilla/5.0 (placeholder)','comment',0,0);
INSERT INTO `wp_comments` (comment_ID,comment_post_ID,comment_author,comment_author_email,comment_author_url,comment_author_IP,comment_date,comment_date_gmt,comment_content,comment_approved,comment_agent,comment_type,comment_parent,user_id) VALUES (108,10,'Commenter G','commenter.g@example.com','','192.0.2.15','2022-03-04 12:00:00','2022-03-04 06:30:00','Reply to a pingback','1','Mozilla/5.0 (placeholder)','comment',104,0);
INSERT INTO `wp_options` VALUES (1,'siteurl','https://blog.example.com','yes');
INSERT INTO `wp_options` VALUES (2,'home','https://blog.example.com','yes');
INSERT INTO `wp_options` VALUES (3,'blogname','Example Blog','yes');
INSERT INTO `wp_options` VALUES (4,'blogdescription','Notes on the economy — and café life','yes');
INSERT INTO `wp_options` VALUES (5,'permalink_structure','/%year%/%monthnum%/%postname%/','yes');
INSERT INTO `wp_options` VALUES (6,'category_base','topics','yes');
INSERT INTO `wp_options` VALUES (7,'tag_base','','yes');
INSERT INTO `wp_options` VALUES (8,'posts_per_page','10','yes');
INSERT INTO `wp_options` VALUES (9,'posts_per_rss','10','yes');
INSERT INTO `wp_options` VALUES (10,'rss_use_excerpt','0','yes');
INSERT INTO `wp_options` VALUES (11,'show_on_front','page','yes');
INSERT INTO `wp_options` VALUES (12,'page_on_front','20','yes');
INSERT INTO `wp_options` VALUES (13,'page_for_posts','21','yes');
INSERT INTO `wp_options` VALUES (14,'sticky_posts','a:1:{i:0;i:10;}','yes');
INSERT INTO `wp_options` VALUES (15,'timezone_string','Asia/Kolkata','yes');
INSERT INTO `wp_options` VALUES (16,'gmt_offset','','yes');
INSERT INTO `wp_options` VALUES (17,'date_format','F j, Y','yes');
INSERT INTO `wp_options` VALUES (18,'time_format','g:i a','yes');
INSERT INTO `wp_options` VALUES (19,'blog_charset','UTF-8','yes');
INSERT INTO `wp_options` VALUES (20,'use_smilies','1','yes');
INSERT INTO `wp_options` VALUES (21,'template','acme','yes');
INSERT INTO `wp_options` VALUES (22,'stylesheet','acme','yes');
INSERT INTO `wp_options` VALUES (23,'db_version','60421','yes');
INSERT INTO `wp_options` VALUES (24,'WPLANG','','yes');
INSERT INTO `wp_options` VALUES (25,'active_plugins','a:6:{i:0;s:39:"acme-guest-author/acme-guest-author.php";i:1;s:19:"akismet/akismet.php";i:2;s:19:"jetpack/jetpack.php";i:3;s:53:"metronet-profile-picture/metronet-profile-picture.php";i:4;s:40:"wordpress-seo-premium/wp-seo-premium.php";i:5;s:25:"wpseo-video/video-seo.php";}','yes');
INSERT INTO `wp_options` VALUES (26,'jetpack_active_modules','a:3:{i:0;s:6:"photon";i:1;s:10:"sharedaddy";i:2;s:5:"stats";}','yes');
INSERT INTO `wp_options` VALUES (27,'default_comment_status','open','yes');
INSERT INTO `wp_options` VALUES (28,'close_comments_for_old_posts','0','yes');
INSERT INTO `wp_options` VALUES (29,'close_comments_days_old','14','yes');
INSERT INTO `wp_options` VALUES (30,'thread_comments','1','yes');
INSERT INTO `wp_options` VALUES (31,'thread_comments_depth','5','yes');
INSERT INTO `wp_options` VALUES (32,'page_comments','0','yes');
INSERT INTO `wp_options` VALUES (33,'comments_per_page','50','yes');
INSERT INTO `wp_options` VALUES (34,'thumbnail_size_w','150','yes');
INSERT INTO `wp_options` VALUES (35,'thumbnail_size_h','150','yes');
INSERT INTO `wp_options` VALUES (36,'thumbnail_crop','1','yes');
INSERT INTO `wp_options` VALUES (37,'medium_size_w','300','yes');
INSERT INTO `wp_options` VALUES (38,'medium_size_h','300','yes');
INSERT INTO `wp_options` VALUES (39,'medium_large_size_w','768','yes');
INSERT INTO `wp_options` VALUES (40,'medium_large_size_h','0','yes');
INSERT INTO `wp_options` VALUES (41,'large_size_w','1024','yes');
INSERT INTO `wp_options` VALUES (42,'large_size_h','1024','yes');
INSERT INTO `wp_options` VALUES (43,'wpseo_titles','a:20:{s:9:"separator";s:7:"sc-dash";s:10:"title-post";s:30:"%%title%% %%sep%% %%sitename%%";s:13:"metadesc-post";s:11:"%%excerpt%%";s:10:"title-page";s:30:"%%title%% %%sep%% %%sitename%%";s:18:"title-tax-category";s:44:"%%term_title%% Archives %%sep%% %%sitename%%";s:18:"title-tax-post_tag";s:35:"%%term_title%% %%sep%% %%sitename%%";s:16:"title-home-wpseo";s:33:"%%sitename%% %%sep%% %%sitedesc%%";s:19:"metadesc-home-wpseo";s:17:"Example Blog home";s:18:"title-author-wpseo";s:29:"%%name%% %%sep%% %%sitename%%";s:19:"title-archive-wpseo";s:29:"%%date%% %%sep%% %%sitename%%";s:18:"title-search-wpseo";s:44:"Search %%searchphrase%% %%sep%% %%sitename%%";s:15:"title-404-wpseo";s:35:"Page not found %%sep%% %%sitename%%";s:20:"noindex-tax-post_tag";b:1;s:20:"noindex-author-wpseo";b:0;s:18:"disable-attachment";b:1;s:12:"disable-date";b:0;s:14:"disable-author";b:0;s:17:"company_or_person";s:7:"company";s:12:"company_name";s:15:"Example Org ₹";s:15:"company_logo_id";i:30;}','yes');
INSERT INTO `wp_options` VALUES (44,'wpseo_social','a:4:{s:13:"facebook_site";s:28:"https://facebook.com/example";s:12:"twitter_site";s:7:"example";s:19:"og_default_image_id";s:2:"30";s:17:"other_social_urls";a:1:{i:0;s:40:"https://www.linkedin.com/company/example";}}','yes');
INSERT INTO `wp_options` VALUES (45,'wpseo','a:1:{s:7:"version";s:4:"24.0";}','yes');
INSERT INTO `wp_options` VALUES (46,'wpseo_taxonomy_meta','a:1:{s:8:"category";a:1:{i:2;a:3:{s:11:"wpseo_title";s:33:"Economy news %%sep%% %%sitename%%";s:10:"wpseo_desc";s:22:"All about the economy.";s:13:"wpseo_noindex";s:7:"default";}}}','yes');
INSERT INTO `wp_options` VALUES (47,'wpseo-premium-redirects-base','a:5:{i:0;a:4:{s:6:"origin";s:8:"old-page";s:3:"url";s:7:"company";s:4:"type";i:301;s:6:"format";s:5:"plain";}i:1;a:4:{s:6:"origin";s:14:"archive/2019/*";s:3:"url";s:1:"/";s:4:"type";i:302;s:6:"format";s:5:"plain";}i:2;a:4:{s:6:"origin";s:17:"^topics-old/(.*)$";s:3:"url";s:9:"topics/$1";s:4:"type";i:301;s:6:"format";s:5:"regex";}i:3;a:4:{s:6:"origin";s:25:"^(?!wp-admin)legacy/(.*)$";s:3:"url";s:1:"/";s:4:"type";i:301;s:6:"format";s:5:"regex";}i:4;a:4:{s:6:"origin";s:9:"gone-page";s:3:"url";s:0:"";s:4:"type";i:410;s:6:"format";s:5:"plain";}}','yes');
INSERT INTO `wp_options` VALUES (48,'wpseo_video','a:1:{s:13:"cloak_sitemap";b:0;}','yes');
INSERT INTO `wp_options` VALUES (49,'theme_mods_acme','a:5:{i:0;b:0;s:18:"nav_menu_locations";a:2:{s:11:"header-menu";i:7;s:11:"footer-menu";i:8;}s:12:"header_color";s:7:"#336699";s:12:"show_tagline";b:1;s:18:"custom_css_post_id";i:-1;}','yes');
INSERT INTO `wp_options` VALUES (50,'widget_text','a:2:{i:2;a:4:{s:5:"title";s:5:"About";s:4:"text";s:40:"<p>About this blog — café edition</p>";s:6:"filter";b:1;s:6:"visual";b:1;}s:12:"_multiwidget";i:1;}','yes');
INSERT INTO `wp_options` VALUES (51,'widget_custom_html','a:2:{i:3;a:2:{s:5:"title";s:0:"";s:7:"content";s:34:"<div class="ad-slot">Ad slot</div>";}s:12:"_multiwidget";i:1;}','yes');
INSERT INTO `wp_options` VALUES (52,'sidebars_widgets','a:3:{s:19:"wp_inactive_widgets";a:0:{}s:9:"sidebar-1";a:2:{i:0;s:6:"text-2";i:1;s:13:"custom_html-3";}s:13:"array_version";i:3;}','yes');
INSERT INTO `wp_options` VALUES (53,'wordpress_api_key','PLACEHOLDER-NOT-A-REAL-KEY','yes');
INSERT INTO `wp_options` VALUES (54,'smtp_password','PLACEHOLDER-NOT-A-REAL-PASSWORD','yes');
INSERT INTO `wp_options` VALUES (55,'cron','a:1:{s:7:"version";i:2;}','yes');
