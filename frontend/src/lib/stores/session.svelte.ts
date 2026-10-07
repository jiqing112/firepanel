/** 会话状态：真实 JWT 认证 + 首次初始化检测。 */
import { backend, setToken, getToken, connectWS, type User } from '$lib/api';
import { navigate } from '$lib/router.svelte';

export const session = $state<{ user: User | null; checked: boolean }>({
  user: null,
  checked: false,
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
  session.checked = true;
  return { needsSetup };
}

export async function login(username: string, password: string) {
  const { token, user } = await backend.login(username, password);
  setToken(token);
  session.user = user;
  connectWS();
  navigate('/dashboard');
}

export function logout() {
  setToken(null);
  session.user = null;
  navigate('/login', true);
}
