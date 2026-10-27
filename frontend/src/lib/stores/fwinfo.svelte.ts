/**
 * 防火墙/系统信息共享状态。
 * Topbar 展示当前后端名；其余页面可按需读取同一份缓存，避免重复请求 /system/info。
 */
import { backend, type SystemInfo } from '$lib/api';

export const fwinfo = $state<{
  backend: string;
  distro: string;
  kernel: string;
  arch: string;
  drift: SystemInfo['drift'];
  ssh_ports: number[];
  loaded: boolean;
}>({
  backend: '',
  distro: '',
  kernel: '',
  arch: '',
  drift: 'unknown',
  ssh_ports: [],
  loaded: false,
});

let loading = false;

/**
 * 加载（并缓存）系统信息。默认只在未加载时请求；force=true 强制刷新。
 * 失败静默——未登录或网络异常时 Topbar 保持占位显示。
 */
export async function loadFwInfo(force = false): Promise<void> {
  if (loading) return;
  if (fwinfo.loaded && !force) return;
  loading = true;
  try {
    const info = await backend.systemInfo();
    fwinfo.backend = info.backend ?? '';
    fwinfo.distro = info.distro ?? '';
    fwinfo.kernel = info.kernel ?? '';
    fwinfo.arch = info.arch ?? '';
    fwinfo.drift = info.drift ?? 'unknown';
    fwinfo.ssh_ports = info.ssh_ports ?? [];
    fwinfo.loaded = true;
  } catch {
    // 保持占位，稍后可重试
  } finally {
    loading = false;
  }
}

/** 清空缓存（登出时调用）。 */
export function resetFwInfo(): void {
  fwinfo.backend = '';
  fwinfo.distro = '';
  fwinfo.kernel = '';
  fwinfo.arch = '';
  fwinfo.drift = 'unknown';
  fwinfo.ssh_ports = [];
  fwinfo.loaded = false;
}
