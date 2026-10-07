/**
 * 文案集中管理（i18n 预留结构）。
 * 当前仅内置 zh-CN 字典；后续接入完整 i18n 时按 key 拆分语言包即可。
 */
export const zh = {
  // 品牌
  'app.name': 'FirePanel',
  'app.slogan': 'Linux 防火墙可视化管理面板',

  // 导航
  'nav.group.overview': '总览',
  'nav.dashboard': '仪表盘',
  'nav.group.traffic': '流量管理',
  'nav.forwards': '端口转发',
  'nav.ports': '端口详情',
  'nav.traffic': '流量监控',
  'nav.firewall': '防火墙规则',
  'nav.iplists': 'IP 名单',
  'nav.proxy': '反向代理',
  'nav.nat': 'NAT 管理',
  'nav.ratelimit': '限速防护',
  'nav.fail2ban': '防爆破(fail2ban)',
  'nav.group.system': '系统',
  'nav.logs': '日志中心',
  'nav.tasks': '定时任务',
  'nav.alerts': '告警通知',
  'nav.backup': '备份迁移',
  'nav.settings': '系统设置',

  // 通用动作
  'action.create': '新增',
  'action.edit': '编辑',
  'action.delete': '删除',
  'action.save': '保存',
  'action.cancel': '取消',
  'action.confirm': '确认',
  'action.search': '搜索',
  'action.refresh': '刷新',
  'action.export': '导出',
  'action.import': '导入',
  'action.more': '更多',
  'action.close': '关闭',
  'action.retry': '重试',
  'action.sync': '同步',

  // 状态
  'state.enabled': '已启用',
  'state.disabled': '已停用',
  'state.active': '生效中',
  'state.pending': '待生效',
  'state.expired': '已过期',
  'state.error': '异常',

  // 仪表盘
  'dashboard.title': '仪表盘',
  'dashboard.subtitle': '系统运行概览与实时流量',
  'dashboard.conn': '活跃连接',
  'dashboard.traffic.today': '今日流量',
  'dashboard.hits.rate': '规则命中 / 分',
  'dashboard.blocked': '已拦截',
  'dashboard.traffic.trend': '流量趋势',
  'dashboard.traffic.rx': '入站',
  'dashboard.traffic.tx': '出站',
  'dashboard.last24h': '近 24 小时',
  'dashboard.hit.rank': '规则命中排行',
  'dashboard.top.ip': 'TOP IP',
  'dashboard.top.ip.sub': '按连接数排序',
  'dashboard.block.action': '封禁',
  'dashboard.sysinfo': '系统信息',
  'dashboard.backend': '防火墙后端',
  'dashboard.rules.count': '生效规则',
  'dashboard.uptime': '运行时长',
  'dashboard.kernel': '内核版本',

  // 端口转发
  'forwards.title': '端口转发',
  'forwards.subtitle': 'TCP / UDP 端口映射与域名目标（DDNS）',
  'forwards.create': '新增转发',
  'forwards.col.rule': '规则',
  'forwards.col.listen': '监听',
  'forwards.col.target': '目标',
  'forwards.col.traffic': '流量',
  'forwards.col.hits': '命中',
  'forwards.col.status': '状态',
  'forwards.col.actions': '操作',
  'forwards.empty.title': '还没有端口转发规则',
  'forwards.empty.desc': '创建第一条规则，将公网端口的流量转发到内网服务。',
  'forwards.search.placeholder': '搜索规则名称、端口或目标…',
  'forwards.filter.all': '全部协议',

  // 登录
  'login.title': '登录 FirePanel',
  'login.username': '用户名',
  'login.password': '密码',
  'login.submit': '登 录',
  'login.error': '用户名或密码错误',
  'login.demo.hint': '原型演示 · 任意账号密码可登录',

  // 占位页
  'placeholder.title': '模块开发中',
  'placeholder.desc': '该模块将在后续阶段交付，当前为原型占位。',
} as const;

export type I18nKey = keyof typeof zh;

const dicts: Record<string, Partial<Record<I18nKey, string>>> = { 'zh-CN': zh };
let locale = 'zh-CN';

export function t(key: I18nKey): string {
  return dicts[locale]?.[key] ?? zh[key] ?? key;
}
