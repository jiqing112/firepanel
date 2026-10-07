/** 与后端共享的领域类型。 */

export type Protocol = 'tcp' | 'udp' | 'both';

export interface ForwardRule {
  id: number;
  name: string;
  protocol: Protocol;
  listen_port: string;
  src_ip: string;
  dst_ip: string;
  dst_domain: string;
  dst_port: string;
  enabled: boolean;
  expires_at: string | null;
  remark: string;
  created_at: string;
  updated_at: string;
}
