package fw

import "testing"

// 入库规范形式：单主机地址不带掩码，网段取网络地址；非法输入为空。
func TestCanonicalIPOrCIDR(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1.2.3.4", "1.2.3.4"},
		{" 1.2.3.4 ", "1.2.3.4"},
		{"1.2.3.4/32", "1.2.3.4"},
		{"10.1.2.3/8", "10.0.0.0/8"},
		{"10.0.0.0/8", "10.0.0.0/8"},
		{"2001:db8::1", "2001:db8::1"},
		{"2001:db8::1/128", "2001:db8::1"},
		{"2001:db8::/32", "2001:db8::/32"},
		{"not-an-ip", ""},
		{"1.2.3.4/99", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := CanonicalIPOrCIDR(c.in); got != c.want {
			t.Errorf("CanonicalIPOrCIDR(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}
