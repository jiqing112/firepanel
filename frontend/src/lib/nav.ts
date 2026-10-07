import {
  LayoutDashboard,
  ArrowRightLeft,
  ShieldBan,
  Globe,
  Network,
  Gauge,
  ShieldAlert,
  ScrollText,
  CalendarClock,
  Bell,
  DatabaseBackup,
  Settings,
  EthernetPort,
  Waves,
  type Icon,
} from '@lucide/svelte';

export interface NavItem {
  path: string;
  labelKey: string;
  icon: typeof Icon;
  /** 所属交付阶段，用于原型阶段的标签展示 */
  phase?: 2 | 3 | 4 | 5;
}

export interface NavGroup {
  labelKey: string;
  items: NavItem[];
}

export const navGroups: NavGroup[] = [
  {
    labelKey: 'nav.group.overview',
    items: [{ path: '/dashboard', labelKey: 'nav.dashboard', icon: LayoutDashboard }],
  },
  {
    labelKey: 'nav.group.traffic',
    items: [
      { path: '/forwards', labelKey: 'nav.forwards', icon: ArrowRightLeft },
      { path: '/ports', labelKey: 'nav.ports', icon: EthernetPort },
      { path: '/traffic', labelKey: 'nav.traffic', icon: Waves },
      { path: '/firewall', labelKey: 'nav.firewall', icon: ShieldBan },
      { path: '/iplists', labelKey: 'nav.iplists', icon: Globe },
      { path: '/proxy', labelKey: 'nav.proxy', icon: Network },
      { path: '/nat', labelKey: 'nav.nat', icon: ArrowRightLeft },
      { path: '/ratelimit', labelKey: 'nav.ratelimit', icon: Gauge },
      { path: '/fail2ban', labelKey: 'nav.fail2ban', icon: ShieldAlert },
    ],
  },
  {
    labelKey: 'nav.group.system',
    items: [
      { path: '/logs', labelKey: 'nav.logs', icon: ScrollText },
      { path: '/tasks', labelKey: 'nav.tasks', icon: CalendarClock },
      { path: '/alerts', labelKey: 'nav.alerts', icon: Bell },
      { path: '/backup', labelKey: 'nav.backup', icon: DatabaseBackup },
      { path: '/settings', labelKey: 'nav.settings', icon: Settings },
    ],
  },
];
