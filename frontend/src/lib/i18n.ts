/**
 * 文案集中管理（i18n 预留结构）。
 * 当前仅内置 zh-CN 字典；后续接入完整 i18n 时按 key 拆分语言包即可。
 * 只收录实际被 t() 消费的键；页面内的静态文案直接写在页面里。
 */
export const zh = {
  // 品牌
  'app.name': 'FirePanel',

  // 导航（导航与页面标题共用同一键）
  'nav.group.overview': '总览',
  'nav.group.traffic': '流量管理',
  'nav.group.system': '系统',
  'nav.dashboard': '仪表盘',
  'nav.forwards': '端口转发',
  'nav.ports': '端口详情',
  'nav.traffic': '流量监控',
  'nav.firewall': '防火墙规则',
  'nav.allow': '端口放行',
  'nav.iplists': 'IP 名单',
  'nav.proxy': '反向代理',
  'nav.nat': 'NAT 管理',
  'nav.ratelimit': '限速防护',
  'nav.fail2ban': '防爆破(fail2ban)',
  'nav.logs': '日志中心',
  'nav.tasks': '定时任务',
  'nav.alerts': '告警通知',
  'nav.backup': '备份迁移',
  'nav.settings': '系统设置',

  // 登录
  'login.title': '登录 FirePanel',
  'login.username': '用户名',
  'login.password': '密码',
  'login.submit': '登 录',
  'login.error': '用户名或密码错误',
} as const;

export type I18nKey = keyof typeof zh;

const dicts: Record<string, Partial<Record<I18nKey, string>>> = { 'zh-CN': zh };
let locale = 'zh-CN';

export function t(key: I18nKey): string {
  return dicts[locale]?.[key] ?? zh[key] ?? key;
}
