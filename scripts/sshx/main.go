// sshx 是开发期使用的最小 SSH 客户端：密码认证，支持远程执行命令与上传文件。
// 用法：
//   sshx -host H -user U -pass P -cmd "uname -a"
//   sshx -host H -user U -pass P -put -local a.bin -remote /tmp/a.bin
//   sshx -host H -user U -pass P -cmd "systemctl status firepanel" -sudo-pass P
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func main() {
	host := flag.String("host", "", "SSH host")
	port := flag.Int("port", 22, "SSH port")
	user := flag.String("user", "", "SSH user")
	pass := flag.String("pass", "", "SSH password")
	cmd := flag.String("cmd", "", "remote command to run")
	sudoPass := flag.String("sudo-pass", "", "sudo password; runs cmd via sudo -S")
	put := flag.Bool("put", false, "upload mode")
	local := flag.String("local", "", "local file to upload")
	remote := flag.String("remote", "", "remote destination path")
	mkdir := flag.Bool("mkdirs", true, "create remote parent directories")
	timeout := flag.Duration("timeout", 180*time.Second, "operation timeout")
	flag.Parse()

	if *host == "" || *user == "" {
		fmt.Fprintln(os.Stderr, "-host and -user are required")
		os.Exit(2)
	}

	cfg := &ssh.ClientConfig{
		User: *user,
		Auth: []ssh.AuthMethod{ssh.Password(*pass)},
		HostKeyCallback: func(string, net.Addr, ssh.PublicKey) error {
			return nil // 开发工具：不做主机 key 校验
		},
		Timeout: 15 * time.Second,
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", *host, *port), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer client.Close()

	switch {
	case *put:
		if *local == "" || *remote == "" {
			fmt.Fprintln(os.Stderr, "-put requires -local and -remote")
			os.Exit(2)
		}
		if err := upload(client, *local, *remote, *mkdir); err != nil {
			fmt.Fprintln(os.Stderr, "put:", err)
			os.Exit(1)
		}
		fmt.Printf("uploaded %s -> %s\n", *local, *remote)
	case *cmd != "":
		run(client, *cmd, *sudoPass, *timeout)
	default:
		fmt.Fprintln(os.Stderr, "nothing to do: need -cmd or -put")
		os.Exit(2)
	}
}

// run 执行远程命令，stdin 注入 sudo 密码（如提供），stdout/stderr 原样透传。
func run(client *ssh.Client, cmd, sudoPass string, timeout time.Duration) {
	full := cmd
	if sudoPass != "" {
		full = "sudo -S -p '' " + cmd
	}
	sess, err := client.NewSession()
	if err != nil {
		fmt.Fprintln(os.Stderr, "session:", err)
		os.Exit(1)
	}
	defer sess.Close()

	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	stdin, err := sess.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stdin:", err)
		os.Exit(1)
	}

	if err := sess.Start(full); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}

	if sudoPass != "" {
		// 同步写入密码，确保 sudo -S 在读 stdin 时能看到它
		if _, err := io.WriteString(stdin, sudoPass+"\n"); err != nil {
			fmt.Fprintln(os.Stderr, "write sudo pass:", err)
		}
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	select {
	case err := <-done:
		stdin.Close()
		if err != nil {
			if ee, ok := err.(*ssh.ExitError); ok {
				os.Exit(ee.ExitStatus())
			}
			fmt.Fprintln(os.Stderr, "wait:", err)
			os.Exit(1)
		}
	case <-time.After(timeout):
		stdin.Close()
		fmt.Fprintln(os.Stderr, "command timeout after", timeout)
		os.Exit(124)
	}
}

func upload(client *ssh.Client, local, remote string, mkdir bool) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sc.Close()

	if mkdir {
		dir := dirOf(remote)
		if dir != "" && dir != "." && dir != "/" {
			parts := splitPath(dir)
			cur := ""
			for _, p := range parts {
				cur = cur + "/" + p
				sc.Mkdir(cur) // 已存在则忽略错误
			}
		}
	}

	dst, err := sc.Create(remote)
	if err != nil {
		return err
	}
	defer dst.Close()

	st, err := f.Stat()
	if err == nil {
		fmt.Printf("uploading %s (%d bytes)...\n", local, st.Size())
	}
	_, err = io.Copy(dst, f)
	return err
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return ""
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(p[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
