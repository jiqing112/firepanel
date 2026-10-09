/** 会话状态：真实 JWT 认证 + 首次初始化检测。 */
import { backend, setToken, getToken, connectWS, type User } from '$lib/api';
import { navigate } from '$lib/router.svelte';

export const session = $state<{ user: User | null; checked: boolean; needsSetup: boolean }>({
  user: null,
  checked: false,
  needsSetup: false,
});

/** 启动时恢复会话：有 token 则校验，无 token 则检查是否需要初始化。 */
export async function initSession(): Promise<{ needsSetup: boolean }> {
  if (getToken()) {
    try {
      const me = await backend.me();
      session.user = { id: me.uid, username: me.username, role: me.role as User['role'], created_at: '' };
      connectWS();
    } catch {
      setToken(null);
    }
  }
  const needsSetup = await backend.needsSetup();
  session.needsSetup = needsSetup;
  session.checked = true;
  return { needsSetup };
}

export async function login(username: string, password: string) {
  const { token, user } = await backend.login(username, password);
  // 先用新 token 验证 /me，确认会话真正可用再进入面板——
  // 避免 WS/竞态触发的 401 把刚登录的状态清掉（表现：登录成功却提示未登录）
  setToken(token);
  const me = await backend.me();
  session.user = { id: me.uid, username: me.username, role: me.role as User['role'], created_at: '' };
  connectWS();
  navigate('/dashboard');
}

export function logout() {
  setToken(null);
  session.user = null;
  navigate('/login', true);
}
