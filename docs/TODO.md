# TODO

1. [ ] 用户增加passkey登录方式,一旦设置passkey,密码登录将被禁用
2. [ ] 增加API Key系统,只有站长才能管理,添加,修改权限和删除API Key, 通过API Key可以代替登录的方式访问后端API,严格控制API Key的权限防止破解
3. [ ] 参考../../Python/PyKYCH完成主题系统,主题只能由站长在网站后台更改
4. [ ] 新增插件系统,插件系统使用单独的插件后端,通过API Key访问主后端API执行操作.插件只能在单独的插件前端进行管理.不对原有的前端和后端做大的改动
5. [ ] Typst编译为PDF,在对应的文章页面放置下载按钮可直接下载PDF
6. [ ] 创建README.md

# P6 — 细节抛光

暗色模式逐项核（border、阴影、stat icon 背景）
移动端 ≤ 768px 体验
加载骨架屏
成功后操作反馈（notice 自动消失 3s）
顺手补 .next/ 到 .gitignore