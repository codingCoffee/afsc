import type { Component } from 'vue'
import type { Cap } from '@/composables/useAuth'
import {
  ArrowRightLeft,
  FileText,
  Files,
  FolderTree,
  Gauge,
  Image,
  ListTree,
  MessageSquare,
  ScrollText,
  Search,
  Settings,
  Table2,
  Tags,
  TriangleAlert,
  UserPen,
  Users,
} from '@lucide/vue'

export interface NavItem {
  label: string
  to: string
  icon: Component
  cap: Cap
}

export interface NavGroup {
  label: string
  items: NavItem[]
}

// every admin screen, grouped as in the sidebar; an item shows only when the user holds its capability
export const navGroups: NavGroup[] = [
  {
    label: 'Content',
    items: [
      { label: 'Dashboard', to: '/dashboard', icon: Gauge, cap: 'self' },
      { label: 'Posts', to: '/posts', icon: FileText, cap: 'posts.edit_own' },
      { label: 'Pages', to: '/pages', icon: Files, cap: 'pages.edit' },
      { label: 'Media', to: '/media', icon: Image, cap: 'posts.edit_own' },
      { label: 'Comments', to: '/comments', icon: MessageSquare, cap: 'comments.moderate' },
    ],
  },
  {
    label: 'Structure',
    items: [
      { label: 'Categories', to: '/categories', icon: FolderTree, cap: 'content.manage' },
      { label: 'Tags', to: '/tags', icon: Tags, cap: 'content.manage' },
      { label: 'Authors', to: '/authors', icon: UserPen, cap: 'content.manage' },
      { label: 'Menus', to: '/menus', icon: ListTree, cap: 'content.manage' },
    ],
  },
  {
    label: 'SEO',
    items: [
      { label: 'SEO settings', to: '/seo/settings', icon: Search, cap: 'settings.manage' },
      { label: 'Redirects', to: '/seo/redirects', icon: ArrowRightLeft, cap: 'content.manage' },
      { label: '404 log', to: '/seo/not-found', icon: TriangleAlert, cap: 'content.manage' },
      { label: 'Bulk editor', to: '/seo/bulk', icon: Table2, cap: 'content.manage' },
    ],
  },
  {
    label: 'Admin',
    items: [
      { label: 'Settings', to: '/settings', icon: Settings, cap: 'settings.manage' },
      { label: 'Users', to: '/users', icon: Users, cap: 'users.manage' },
      { label: 'Audit log', to: '/audit', icon: ScrollText, cap: 'audit.read' },
    ],
  },
]

export function useNav() {
  const { can } = useAuth()
  return computed(() => navGroups
    .map(g => ({ ...g, items: g.items.filter(i => can(i.cap)) }))
    .filter(g => g.items.length))
}
