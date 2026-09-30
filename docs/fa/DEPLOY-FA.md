<div dir="rtl">

# نصب روی سرور واقعی، قدم به قدم

این راهنما نصب کامل git-policy روی **سروری است که GitLab (Docker) روی آن اجرا می‌شود**، به‌همراه Jenkins به‌عنوان رابط کاربری. همین مراحل روی سرور `192.168.120.128` اجرا و با تست پذیرش (**۳۷ از ۳۷**) تأیید شده است.

> نوشتن policy: [POLICY-FA.md](POLICY-FA.md) · همه‌ی دستورات: [CLI-FA.md](CLI-FA.md) · راهنمای کلی: [GUIDE-FA.md](GUIDE-FA.md)

---

## ۱. چه چیزی نصب می‌شود؟

<div dir="ltr">

```
                 سرور GitLab (Docker host)
 ┌──────────────────────────────────────────────────────────────────┐
 │  کانتینر gitlab                                                  │
 │    pre-receive.d/50-git-policy ──> /var/opt/gitlab/git-policy/   │
 │                                      bin/ policies/ state/ logs/ │
 │        ▲ docker exec (فقط از طریق git-policy-ctl)                 │
 │  /usr/local/sbin/git-policy-ctl  <── sudo <── کاربر gitpolicy-deploy │
 │        ▲ SSH با forced command و host key ثابت (pin)              │
 │  کانتینر gp-jenkins (پورت 8081)  ── job ها: git-policy، gitops، sync │
 └──────────────────────────────────────────────────────────────────┘
          │ clone
 GitLab: platform/git-policy-config  (test/policy.yaml، production/policy.yaml)
```

</div>

| جزء | کجا | چه کسی نصب می‌کند |
|---|---|---|
| موتور git-policy + hook | داخل کانتینر `gitlab` | `install.sh` |
| کانال کنترل (`gitpolicy-deploy`، sudoers، `git-policy-ctl`) | روی host | `setup.sh` ← `deploy/install-ctl.sh` |
| token فقط‌خواندنی GitLab برای Jenkins | GitLab (کاربر root، `read_api` + `read_repository`، ۳۶۵ روز) | `setup.sh` |
| repository ‌GitOps: `platform/git-policy-config` | GitLab | `setup.sh` |
| Jenkins (کانتینر `gp-jenkins`) | روی host، پورت 8081 | `setup.sh` |

---

## ۲. پیش‌نیازها

- GitLab CE در Docker با نام کانتینر `gitlab` (نام دیگر: گزینه‌ی `--container`).
- در `GITLAB_OMNIBUS_CONFIG` باید این تنظیم باشد (روی سرور فعلی از قبل هست):

<div dir="ltr">

```ruby
gitaly['configuration'] = { hooks: { custom_hooks_dir: '/var/opt/gitlab/gitaly/custom_hooks' } }
```

</div>

- `/var/opt/gitlab` روی volume باشد (بررسی خودکار).
- روی host: `docker` (با compose)، `jq`، `python3`، `git`، `curl`، `sudo`، `ssh`، و دسترسی root.
- دسترسی اینترنت (یا mirror) **فقط یک بار** برای ساخت image ‌Jenkins (دانلود pluginها).

---

## ۳. مراحل

### قدم ۱: ساخت باینری (روی ماشین توسعه)

<div dir="ltr">

```bash
cd git-policy
./tools/build.sh all          # test + build -> dist/git-policy-0.1.0-linux-amd64 (+ .sha256)
```

</div>

### قدم ۲: کپی پروژه روی سرور

<div dir="ltr">

```bash
# از ماشین توسعه (فایل‌های git + باینری):
{ git ls-files; ls dist/git-policy-*-linux-amd64*; } | tar -cf - -T - \
  | ssh root@SERVER 'mkdir -p /opt/git-policy && tar -xf - -C /opt/git-policy'
ssh root@SERVER 'cd /opt/git-policy && sha256sum -c dist/*.sha256'
```

</div>

### قدم ۳: نصب موتور (روی سرور، با root)

<div dir="ltr">

```bash
cd /opt/git-policy
./install.sh --dry-run --actor "install.sh:YOUR_NAME"   # فقط بررسی
./install.sh --actor "install.sh:YOUR_NAME"             # نصب
```

</div>

- موتور **خاموش** نصب می‌شود؛ رفتار push هنوز هیچ تغییری نکرده است.
- hookهای قبلی (مثل `01-block-dll`) دست نمی‌خورند.
- اگر پوشه‌ی `pre-receive.d` وجود نداشته باشد، ساخته می‌شود.

### قدم ۴: کانال کنترل + Jenkins + repository ‌GitOps

<div dir="ltr">

```bash
deploy/jenkins/setup.sh --address SERVER_IP --dry-run
deploy/jenkins/setup.sh --address SERVER_IP
```

</div>

| گزینه | پیش‌فرض | توضیح |
|---|---|---|
| `--address HOST` | **الزامی** | آدرس همین سرور، همان‌طور که Jenkins (داخل کانتینر) آن را می‌بیند |
| `--gitlab-url URL` | `http://HOST:8080` | آدرس GitLab |
| `--container NAME` | `gitlab` | کانتینر GitLab |
| `--jenkins-port N` | `8081` | پورت Jenkins |
| `--jenkins-image IMG` | `jenkins/jenkins:lts-jdk17` | image پایه (روی سرور فعلی: `jenkins/jenkins:2.568.3-lts` که از قبل موجود بود) |
| `--dry-run` | — | فقط بررسی |

این اسکریپت ۹ مرحله دارد و **اجرای دوباره‌اش بی‌خطر است**:

1. بررسی پیش‌نیازها و در دسترس بودن GitLab
2. ساخت رمزهای Jenkins و کلید SSH در `deploy/jenkins/.secrets/` (فقط root، 0700)
3. نصب کانال کنترل (`deploy/install-ctl.sh`): کاربر `gitpolicy-deploy` بدون رمز و **بدون** گروه docker، sudoers محدود به یک دستور، کلید با `command=…,restrict`
4. **pin کردن host key**: کلید اسکن‌شده باید دقیقاً کلید همین سرور باشد؛ سپس تست کانال
5. token فقط‌خواندنی GitLab. خود GitLab token را می‌سازد و فقط در فایل نوشته می‌شود؛ هیچ‌جا چاپ نمی‌شود
6. ساخت `platform/git-policy-config` با محتوای اولیه (`deploy/policy-repo-seed/`) با یک token موقت که بلافاصله باطل می‌شود
7. ساخت image ‌Jenkins و فایل `.env` (بدون هیچ secret)
8. انتقال secretها به یک volume که فقط کاربر jenkins داخل کانتینر می‌تواند بخواند (secretها در `env` pipelineها **دیده نمی‌شوند**)
9. بالا آوردن Jenkins و یک بار اجرای هر job (برای ثبت پارامترها و زمان‌بندی). job ‌gitops در همین‌جا `test/policy.yaml` را اعمال می‌کند

### قدم ۵: روشن کردن (از Jenkins)

`http://SERVER_IP:8081` ← job ‌**git-policy** ← **Build with Parameters**:

| ترتیب | پارامترها | نتیجه |
|---|---|---|
| ۱ | `ACTION=STATUS` | policy فعال: `org-policy-test revision 1`، موتور خاموش |
| ۲ | `ACTION=ENABLE`، `ENVIRONMENT=TEST`، `REASON=…` | `engine ENABLED` |
| ۳ | `ACTION=EXPLAIN`، `EXPLAIN_USER=…`، `EXPLAIN_PROJECT=…` | فهرست قوانین مؤثر (باید `dll` و `exe` در آن باشد) |
| ۴ | `ACTION=RETIRE_HOOK`، `HOOK_NAME=01-block-dll`، `REASON=…` | hook قدیمی به `backup/` منتقل می‌شود. فقط وقتی که git-policy همان کار را انجام می‌دهد |

### قدم ۶: تست پذیرش

<div dir="ltr">

```bash
cd /opt/git-policy && tests/deploy/acceptance.sh --address SERVER_IP
```

</div>

- **هیچ چیزی را reset یا خراب نمی‌کند.** یک کاربر، گروه و پروژه‌ی موقت (`gpacc-*`، `gp-acceptance-*`) می‌سازد و در پایان حذف می‌کند.
- ۳۷ بررسی دارد:
  - کانال کنترل: host key، forced command، تزریق دستور
  - همه‌ی اکشن‌های Jenkins
  - ۹ نوع push: پسوند، بزرگی حروف، PE تغییرنام‌یافته، مسیر، حجم، فایل پنهان در تاریخچه، commit از Web/API
  - audit log
  - تأیید چهار چشم در PRODUCTION
  - نبود secret در لاگ buildها
- نتیجه‌ی مورد انتظار: `RESULT: 37 passed, 0 failed`

---

## ۴. رمزها و فایل‌های مهم روی سرور

| چه چیزی | کجا |
|---|---|
| رمز Jenkins ‌`admin` | `sudo cat /opt/git-policy/deploy/jenkins/.secrets/jenkins-admin-password` |
| رمز Jenkins ‌`approver` (نفر دوم برای PRODUCTION) | `sudo cat /opt/git-policy/deploy/jenkins/.secrets/jenkins-approver-password` |
| کلید SSH ‌Jenkins، host key ثابت‌شده، token ‌GitLab | همان پوشه‌ی `.secrets/` (در git نیست) |
| تنظیمات Jenkins (بدون secret) | `deploy/jenkins/.env` |
| لاگ هر فراخوانی کانال کنترل | `/var/log/git-policy-ctl.log` |
| داده‌های موتور | داخل کانتینر: `/var/opt/gitlab/git-policy/` |

**تغییر رمز Jenkins:** فایل رمز را عوض کنید، `setup.sh` را دوباره اجرا کنید و سپس `docker restart gp-jenkins` (Jenkins تنظیمات را فقط هنگام شروع می‌خواند).

**رمز root گیت‌لب:** فایل `/etc/gitlab/initial_root_password` فقط رمز **اولیه** را نگه می‌دارد و بعد از تغییر رمز دیگر معتبر نیست. برای reset:

<div dir="ltr">

```bash
docker exec -it gitlab gitlab-rake "gitlab:password:reset[root]"
```

</div>

---

## ۵. نکته‌ی سرور تک‌GitLab

روی `192.168.120.128` فقط یک GitLab هست. به همین دلیل TEST و PRODUCTION در Jenkins به **همان سرور** اشاره می‌کنند:

- job ‌`git-policy-gitops` همیشه برای PRODUCTION «drift» گزارش می‌دهد (build زرد). این مورد انتظار است.
- اگر `production/policy.yaml` را promote کنید، همان روی این سرور فعال می‌شود. `revision` آن باید از revision فعال بزرگ‌تر باشد.
- در سازمان واقعی با دو GitLab جدا، روی **هر** سرور قدم‌های ۳ و ۴ را اجرا کنید و در Jenkins اصلی متغیرهای `GP_CTL_PRODUCTION` و `GP_GITLAB_URL_PRODUCTION` را به سرور PRODUCTION تغییر دهید.

---

## ۶. برگرداندن

| کار | دستور |
|---|---|
| خاموش کردن موقت | Jenkins: `ACTION=DISABLE`، `DISABLE_TTL=…` |
| برگرداندن hook قدیمی | `docker exec -u root gitlab sh -c 'cp /var/opt/gitlab/git-policy/backup/01-block-dll.retired.* /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll && chmod 0755 /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll'` |
| حذف hook ‌git-policy (داده‌ها می‌مانند) | `./uninstall.sh` |
| توقف Jenkins | `docker compose -f deploy/jenkins/docker-compose.yml --env-file deploy/jenkins/.env down` |
| اضطراری بدون Jenkins | break-glass: [CLI-FA.md](CLI-FA.md) بخش ۴ |

---

## ۷. مشکلاتی که در نصب واقعی پیدا و رفع شد

| مشکل | رفع |
|---|---|
| روی GitLab تازه پوشه‌ی `pre-receive.d` وجود ندارد و `install.sh` متوقف می‌شد | خودش می‌سازد (`root:root 0755`) |
| وقتی Jenkins برای build هنوز شروع‌نشده 404 می‌داد، `setup.sh` بی‌صدا با کد ۵ خارج می‌شد | polling تا شروع build ادامه می‌دهد |
| GitLab رمزی را که شامل نام کاربری باشد رد می‌کند (کاربر موقت تست) | رمز تصادفی |
| رمز root در `initial_root_password` بعد از تغییر رمز معتبر نیست | در بخش ۴ توضیح داده شد |

</div>
