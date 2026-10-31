# 界面原型截图

面板各页面的视觉参考图，共 34 张。

覆盖范围：亮/暗主题的仪表盘、端口转发、登录页，以及防火墙规则、IP 名单、日志、
备份、限速、NAT / SNAT、定时任务、告警、设置、防爆破等页面形态。

文件名即含义，读法：

| 前缀 / 后缀 | 含义 |
|---|---|
| `vm-` | 在虚拟机里实际跑起来后的截图 |
| `us-` | 线上环境的截图 |
| `light` / `dark` | 主题变体 |
| `radio` / `colon` / `modal` / `fixed` / `restructured` | 同一页面的迭代版本，用于对比改版前后 |

## 为什么不在仓库里

这批图约 7.9 MB。PNG 本身不可再压缩，而 git 一旦提交就永久留在历史里——
删掉文件也不会让仓库变小，每个 clone 的人都要多拉这份体积。
所以改由 Release 附件分发，仓库里只留这份说明。

## 下载

https://github.com/jiqing112/firepanel/releases/download/v1.0.5/prototype-screenshots.zip

解压得到 `prototype/` 目录。

## 更新截图

界面改动后如需重拍，直接替换本地 `docs/prototype/` 下的文件，再打包上传到对应 Release。
该目录已在 `.gitignore` 中，不会被误提交。
