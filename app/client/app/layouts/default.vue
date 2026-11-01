<script setup lang="ts">
import { LogOut, UserRound } from '@lucide/vue'
import { computed } from 'vue'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
} from '@/components/ui/breadcrumb'
import CspReportOnlyBanner from '@/components/settings/CspReportOnlyBanner.vue'
import { Separator } from '@/components/ui/separator'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from '@/components/ui/sidebar'

const { user, signout } = useAuth()
const nav = useNav()
const route = useRoute()

const current = computed(() => {
  for (const g of nav.value) {
    const hit = g.items.find(i => route.path === i.to || route.path.startsWith(`${i.to}/`))
    if (hit)
      return hit.label
  }
  return route.path === '/profile' ? 'Profile' : ''
})

function active(to: string) {
  return route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <SidebarProvider>
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <NuxtLink to="/dashboard" class="flex h-8 items-center px-2 font-display text-sm tracking-[0.2em] hover:text-primary">
          AFSC
        </NuxtLink>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup v-for="g in nav" :key="g.label">
          <SidebarGroupLabel class="font-mono text-[10px] tracking-widest uppercase">
            {{ g.label }}
          </SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem v-for="item in g.items" :key="item.to">
                <SidebarMenuButton as-child :is-active="active(item.to)" :tooltip="item.label">
                  <NuxtLink :to="item.to">
                    <component :is="item.icon" />
                    <span>{{ item.label }}</span>
                  </NuxtLink>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton as-child :is-active="route.path === '/profile'" tooltip="Profile">
              <NuxtLink to="/profile">
                <UserRound />
                <span class="truncate">{{ user?.display_name || user?.username }}</span>
              </NuxtLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton tooltip="Sign out" @click="signout">
              <LogOut />
              <span>Sign out</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>

    <SidebarInset>
      <header class="sticky top-0 z-30 flex h-14 items-center gap-2 border-b bg-background/85 px-4 backdrop-blur-md">
        <SidebarTrigger class="-ml-1" />
        <Separator orientation="vertical" class="mr-1 h-4" />
        <Breadcrumb>
          <BreadcrumbList>
            <BreadcrumbItem>
              <BreadcrumbPage class="font-mono text-[11px] tracking-widest uppercase">
                {{ current }}
              </BreadcrumbPage>
            </BreadcrumbItem>
          </BreadcrumbList>
        </Breadcrumb>
        <div class="ml-auto">
          <ThemeToggle />
        </div>
      </header>
      <main class="mx-auto w-full max-w-7xl flex-1 px-4 py-6 sm:px-8 sm:py-8">
        <CspReportOnlyBanner />
        <slot />
      </main>
    </SidebarInset>
  </SidebarProvider>
</template>
