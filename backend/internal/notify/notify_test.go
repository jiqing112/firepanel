package notify

import "testing"

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		raw     string
		wantErr bool
	}{
		{"telegram ok", "telegram", `{"bot_token":"123:abc","chat_id":"-100"}`, false},
		{"telegram chat_id 为数字", "telegram", `{"bot_token":"123:abc","chat_id":123}`, true},
		{"telegram 缺 token", "telegram", `{"chat_id":"1"}`, true},
		{"webhook ok", "webhook", `{"url":"https://example.com/hook","method":"POST"}`, false},
		{"webhook 非 http", "webhook", `{"url":"ftp://example.com/hook"}`, true},
		{"webhook 缺 host", "webhook", `{"url":"http://"}`, true},
		{"webhook method 非法", "webhook", `{"url":"http://x/y","method":"DELETE"}`, true},
		{"smtp ok", "smtp", `{"host":"smtp.example.com","to":"a@b.c","port":587}`, false},
		{"smtp 缺收件人", "smtp", `{"host":"smtp.example.com"}`, true},
		{"未知渠道", "irc", `{}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateConfig(c.channel, []byte(c.raw))
			if (err != nil) != c.wantErr {
				t.Fatalf("ValidateConfig() err=%v，期望错误=%v", err, c.wantErr)
			}
		})
	}
}
