<div dir="rtl">

# راهنمای کامل دستورات git-policy

این سند **همه‌ی دستورات و گزینه‌ها** را توضیح می‌دهد:

- باینری `git-policy`
- `git-policy-ctl`، کانال Jenkins
- `install.sh` و `uninstall.sh`
- lab محلی
- اسکریپت‌های build و تست

همه‌ی گزینه‌ها از خروجی خود برنامه (`git-policy help` و `-h` هر دستور) استخراج شده‌اند.

> نسخه: **v0.1.0** — schema سیاست: **`git-policy/v1`**

---

## ۱. قراردادها

| موضوع | توضیح |
|---|---|
| شکل گزینه | هم `--json` و هم `-json` قبول است. مقدار با فاصله یا `=` داده می‌شود: `--ttl 2h` یا `--ttl=2h` |
| **ترتیب** | **گزینه‌ها باید قبل از نام فایل بیایند.** `validate --json p.yaml` درست است، ولی در `validate p.yaml --json` گزینه‌ی `--json` نادیده گرفته می‌شود |
| `-` به جای فایل | یعنی خواندن از stdin، مثلاً `admin apply -` |
| مسیر نصب | پیش‌فرض `/var/opt/gitlab/git-policy`؛ با `--root DIR` قابل تغییر است (برای تست) |
| دستورات `admin` | روی مسیر پیش‌فرض فقط با **root** داخل کانتینر GitLab اجرا می‌شوند: `docker exec -u root gitlab …`. در عملیات واقعی از **Jenkins** اجرا می‌شوند (بخش ۵) |

### کدهای خروج

| کد | معنی | کجا |
|---|---|---|
| `0` | موفق | همه |
| `1` | policy نامعتبر، push رد شد، یا عملیات رد شد (مثلاً enable بدون policy) | همه |
| `2` | خطای استفاده یا خطای فایل/ورودی | همه |
| `0 / 1 / 3` | `OK` / `WARNING` / `CRITICAL` | فقط `status` |

---

## ۲. دستورات عمومی

### `git-policy version`

نسخه‌ی نرم‌افزار، نسخه‌ی schema، commit، زمان build و پلتفرم را نشان می‌دهد.

| گزینه | توضیح |
|---|---|
| `--json` | خروجی ماشینی |

<div dir="ltr">

```bash
git-policy version
git-policy version --json
```

</div>

---

### `git-policy validate [گزینه‌ها] <policy.yaml | ->`

فایل policy را **کامل** بررسی می‌کند (syntax، کلید ناشناخته، نوع، و همه‌ی کدهای V و W) بدون اینکه چیزی را تغییر دهد.

| گزینه | مقدار | پیش‌فرض | توضیح |
|---|---|---|---|
| `--json` | — | خاموش | خروجی JSON (برای Jenkins): `valid`، `errors`، `warnings`، `sha256`، `findings` |
| `--now` | `YYYY-MM-DD` | امروز | تاریخ مرجع برای بررسی انقضای استثناها (برای تست یا پیش‌بینی) |
| `--inventory` | فایل | — | inventory از `sync-membership --inventory-out`؛ فعال کردن هشدار **W010** برای کاربر، گروه یا پروژه‌ای که در GitLab نیست |

<div dir="ltr">

```bash
git-policy validate policy.yaml
git-policy validate --json policy.yaml
cat policy.yaml | git-policy validate -
git-policy validate --now 2026-12-01 policy.yaml     # «این policy در ۱ دسامبر معتبر است؟»
```

</div>

خروجی نمونه:

<div dir="ltr">

```
git-policy validate: policy.yaml
  policy:   org-git-policy (revision 3)
  sha256:   8358935b...
  WARNING W001  mandatory.blocked_extensions[0]: leading dot in ".dll" is ignored
RESULT: VALID (0 errors, 1 warnings)
```

</div>

---

### `git-policy compile [گزینه‌ها] <policy.yaml | ->`

policy را validate می‌کند و نسخه‌ی نرمال‌شده‌ی آن (`compiled.json`، همان چیزی که hook می‌خواند) را چاپ می‌کند. برای عیب‌یابی است؛ استقرار واقعی با `admin apply` انجام می‌شود.

| گزینه | مقدار | توضیح |
|---|---|---|
| `-o` | مسیر فایل | به جای stdout در فایل بنویس |
| `--now` | `YYYY-MM-DD` | مثل validate |

<div dir="ltr">

```bash
git-policy compile policy.yaml | jq .mandatory
git-policy compile -o /tmp/compiled.json policy.yaml
```

</div>

---

### `git-policy status [گزینه‌ها]`

گزارش سلامت فقط-خواندنی:
- وضعیت موتور و دلیل آن
- break-glass
- سالم بودن فایل hook و hookهای دیگر
- policy فعال (نسخه، checksum، چه کسی، کی)، نسخه‌ی قبلی، تعداد نسخه‌ها
- cache عضویت
- audit log

| گزینه | مقدار | پیش‌فرض | توضیح |
|---|---|---|---|
| `--json` | — | — | خروجی ماشینی (فیلدهای `health`، `problems`، `engine`، `hook`، `policy`، …) |
| `--root` | مسیر | `/var/opt/gitlab/git-policy` | مسیر نصب |
| `--hook-path` | مسیر | `…/pre-receive.d/50-git-policy` | فایل hook برای بررسی سلامت |

کد خروج: `0` OK، `1` WARNING (مثلاً موتور خاموش)، `3` CRITICAL (مثلاً hook نصب نیست، break-glass فعال، موتور روشن بدون policy).

<div dir="ltr">

```bash
docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy status
```

</div>

---

### `git-policy explain [گزینه‌ها]`

**بدون push واقعی** نشان می‌دهد policy برای یک push فرضی چه تصمیمی می‌گیرد و چرا:
- کدام قوانین هویتی مطابقت داشتند و کدام تصمیم گرفت
- کدام استثنا اعمال شد
- قوانین محتوایی مؤثر برای آن پروژه و شاخه

| گزینه | مقدار | پیش‌فرض | توضیح |
|---|---|---|---|
| `--project` | `group/project` | **الزامی** | مسیر پروژه |
| `--user` | نام کاربری | خالی = `@unknown` | کاربر push‌کننده |
| `--groups` | `a,b,c` | از cache عضویت | گروه‌های کاربر (برای تست بدون cache) |
| `--ref` | `refs/...` | `refs/heads/main` | شاخه یا تگ |
| `--repository` | `project-N`، `wiki-N`، … | خالی = project | نوع repository |
| `--policy` | فایل | policy فعال سرور | بررسی یک فایل policy به جای نسخه‌ی فعال |
| `--root` | مسیر | پیش‌فرض | برای خواندن policy فعال و cache |
| `--json` | — | — | خروجی ماشینی (`verdict`، `identity`، `content`، …) |

<div dir="ltr">

```bash
git-policy explain --policy p.yaml --user alex --project finance/payment-api
git-policy explain --user carol --groups contractors --project finance/app --ref refs/heads/release/1.0
```

</div>

---

### `git-policy scan [گزینه‌ها] < ref-updates` (عیب‌یابی)

داخل یک repository اجرا می‌شود و نشان می‌دهد یک push **دقیقاً چه چیزهایی** وارد می‌کند (commitهای جدید، مسیرها، blobها) به‌علاوه‌ی تخلف‌ها. ورودی همان خطوط `<old> <new> <ref>` است که pre-receive می‌گیرد.

| گزینه | مقدار | پیش‌فرض | توضیح |
|---|---|---|---|
| `--policy` | فایل | policy فعال | policy برای ارزیابی |
| `--project` | مسیر | `$GL_PROJECT_PATH` | پروژه |
| `--user` | نام | `$GL_USERNAME` | کاربر |
| `--repo` | پوشه | پوشه‌ی فعلی | مسیر repository |
| `--root` | مسیر | پیش‌فرض | برای policy فعال |
| `--json` | — | — | خروجی ماشینی |

<div dir="ltr">

```bash
cd /path/to/repo.git
echo "<old-sha> <new-sha> refs/heads/main" | git-policy scan --policy p.yaml --project finance/app
```

</div>

---

### `git-policy groups [--policy FILE] [--root DIR]`

گروه‌های GitLab را که policy از آن‌ها استفاده می‌کند چاپ می‌کند، هر کدام در یک خط: قوانین `groups`، `mandatory.deny_groups` و `subjects.groups` استثناها. همان چیزی است که sync باید بخواند.

| گزینه | توضیح |
|---|---|
| `--policy` | فایل policy (پیش‌فرض: policy فعال سرور) |
| `--root` | مسیر نصب برای خواندن policy فعال |

---

### `git-policy sync-membership` (روی agent جنکینز)

عضویت گروه‌ها را از GitLab API می‌خواند و `membership.json` می‌سازد (و در صورت نیاز inventory). **token فقط از متغیر `GITLAB_TOKEN` یا فایل خوانده می‌شود و هرگز چاپ نمی‌شود.**

| گزینه | مقدار | پیش‌فرض | توضیح |
|---|---|---|---|
| `--gitlab-url` | `https://gitlab.example.com` | **الزامی** | آدرس GitLab (بدون `/api/v4`) |
| `--policy` | فایل | — | گروه‌ها از این policy خوانده شوند |
| `--groups` | فهرست (جداشده با کاما، فاصله یا خط جدید) | — | یا گروه‌ها مستقیم (مثلاً خروجی `ctl groups`). یکی از این دو الزامی است |
| `-o` | فایل | stdout | محل نوشتن membership.json |
| `--inventory-out` | فایل | — | inventory کاربران، گروه‌ها و پروژه‌ها (برای W010) |
| `--token-file` | فایل | `$GITLAB_TOKEN` | فایل حاوی token؛ فاصله و newline انتها حذف می‌شود |
| `--ca-file` | فایل | — | CA اضافه برای TLS |
| `--insecure` | — | خاموش | نادیده گرفتن TLS (**فقط lab**) |
| `--timeout` | مدت | `30s` | timeout هر درخواست |

- **رفتار در خطا:** گروه ناموجود، token نامعتبر (401/403) یا GitLab در دسترس نبودن → کد خروج ۱ و **هیچ فایلی** نوشته نمی‌شود.
- پاسخ 429 یا 5xx تا ۵ بار با backoff و `Retry-After` دوباره امتحان می‌شود.
- token باید دسترسی `read_api` داشته باشد. برای inventory کامل، token باید admin باشد.

---

### `git-policy show-policy [--version N] [--root DIR]`

متن **دقیق** `policy.yaml` نسخه‌ی فعال (یا نسخه‌ی `N`) را چاپ می‌کند؛ Jenkins از آن برای diff استفاده می‌کند. قبل از چاپ، checksum نسخه بررسی می‌شود.

---

### `git-policy hook [--root DIR]`

همان دستوری که hook گیت‌لب اجرا می‌کند. **به‌صورت دستی استفاده نمی‌شود.** stdin را از Git و متغیرهای `GL_*` را از Gitaly می‌گیرد.

| گزینه | توضیح |
|---|---|
| `--root` | مسیر نصب؛ اسکریپت hook خودش آن را می‌فرستد |

متغیرهایی که hook می‌خواند (همه را Gitaly تنظیم می‌کند، نه کاربر):

| متغیر | نمونه‌ی واقعی (از lab) |
|---|---|
| `GL_USERNAME` | `bob`؛ برای deploy key، نام **سازنده‌ی** کلید |
| `GL_ID` | `user-3` یا `key-10` (deploy key) |
| `GL_PROJECT_PATH` | `finance/app`؛ برای wiki: `finance/app.wiki` |
| `GL_REPOSITORY` | `project-27`، `wiki-27`، `snippet-N`، `design-N` |
| `GL_PROTOCOL` | `http`، `ssh`، `web` (Web UI، API، merge کردن MR) |

متغیرهای `GIT_PUSH_OPTION_*` (یعنی `git push -o`) را کاربر کنترل می‌کند و **عمداً خوانده نمی‌شوند**.

---

## ۳. دستورات مدیریتی (`git-policy admin …`)

### گزینه‌های مشترک همه‌ی دستورات admin

| گزینه | مقدار | پیش‌فرض | توضیح |
|---|---|---|---|
| `--root` | مسیر | `/var/opt/gitlab/git-policy` | مسیر نصب. روی مسیر پیش‌فرض **فقط root** اجرا می‌کند |
| `--hook-path` | مسیر | `/var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/50-git-policy` | محل فایل hook |
| `--actor` | متن (حروف، عدد، `_ .:#@/+=-` و فاصله، تا ۱۲۸ کاراکتر) | `sudo:USER` یا `local:USER` | چه کسی این کار را کرد؛ در audit log ثبت می‌شود. Jenkins مثلاً `jenkins#57 approved-by:ali` می‌فرستد |
| `--reason` | متن تا ۵۰۰ کاراکتر، بدون کاراکتر کنترلی | — | چرا؛ در audit log ثبت می‌شود |

هر دستور admin:
- یک رویداد در audit log ثبت می‌کند.
- با یک قفل (`state/admin.lock`) از اجرای هم‌زمان دو عملیات مدیریتی جلوگیری می‌کند (حداکثر ۱۰ ثانیه انتظار).

---

### `admin install [--replace-hook]`

باینری در حال اجرا را نصب یا ارتقا می‌دهد، پوشه‌ها را با مالکیت و مجوز درست می‌سازد، state اولیه را ایجاد می‌کند و فایل hook را قرار می‌دهد.

| گزینه | توضیح |
|---|---|
| `--replace-hook` | اگر `50-git-policy` با محتوای **متفاوت** وجود دارد، از آن backup بگیر و جایگزینش کن. بدون این گزینه نصب متوقف می‌شود |

- اولین نصب: موتور **خاموش** است، پس نصب هیچ تغییری در رفتار push ایجاد نمی‌کند.
- ارتقا: باینری قبلی به `bin/git-policy.previous` می‌رود؛ policy و state و لاگ‌ها حفظ می‌شوند.
- hookهای دیگر (مثلاً PoC) **دست‌نخورده** می‌مانند.
- رویداد audit: `ENGINE_INSTALLED`.

---

### `admin uninstall [--force]`

فقط فایل hook را حذف می‌کند (با backup در `backup/`). policy، state، لاگ‌ها و باینری باقی می‌مانند.

| گزینه | توضیح |
|---|---|
| `--force` | حتی اگر فایل hook دستی تغییر کرده باشد، حذفش کن |

رویداد audit: `HOOK_UNINSTALLED`.

---

### `admin retire-hook --name FILE`

یک hook **دیگر** در `pre-receive.d` (مثلاً PoC `01-block-dll`) را به `backup/` منتقل می‌کند.

| گزینه | توضیح |
|---|---|
| `--name` | نام فایل؛ فقط حروف، عدد و `._-`. مسیر (`../`) و نام hook خود git-policy قبول **نمی‌شوند** |

برگرداندن: فایل backup را کپی کنید و `chmod 0755` بزنید. رویداد audit: `HOOK_RETIRED`.

---

### `admin enable [--reason TEXT]`

موتور را روشن می‌کند. **رد می‌شود** اگر:
- هیچ policy سالمی مستقر نشده باشد (چون همه‌ی pushها رد می‌شدند)
- نسخه‌ی فعال خراب باشد و موتور روی نسخه‌ی قبلی (fallback) کار کند
- policy قانون deny گروهی داشته باشد ولی cache عضویت موجود نباشد

رویداد audit: `POLICY_ENABLED`.

---

### `admin disable --reason TEXT --ttl DURATION`

موتور را **موقتاً** خاموش می‌کند.

| گزینه | مقدار | توضیح |
|---|---|---|
| `--reason` | **الزامی** | دلیل خاموشی |
| `--ttl` | **الزامی**؛ `30s`، `45m`، `2h` (حداکثر `168h`) | بعد از این مدت موتور **خودکار روشن** می‌شود |

خاموشی بدون زمان انقضا ممکن نیست، پس خاموشی فراموش‌شده هم پیش نمی‌آید. رویداد audit: `POLICY_DISABLED`.

---

### `admin apply [--reason TEXT] <policy.yaml | ->`

یک policy جدید را مستقر و فعال می‌کند:
1. validate کامل
2. `metadata.revision` باید از نسخه‌ی فعال **بزرگ‌تر** باشد (وگرنه `V040`)
3. ساخت پوشه‌ی نسخه‌ی جدید (`00000N`، فقط‌خواندنی)
4. فعال‌سازی با rename اتمیک؛ نسخه‌ی قبلی `PREVIOUS` می‌شود

- policy نامعتبر **هیچ‌چیزی را تغییر نمی‌دهد**.
- اگر موتور روشن باشد و policy قانون deny گروهی داشته باشد ولی cache عضویت نباشد، با `V042` رد می‌شود.
- رویدادهای audit: `POLICY_UPDATED` یا `POLICY_UPDATE_REFUSED` (به‌همراه کدهای خطا).

<div dir="ltr">

```bash
git-policy admin apply --actor ali --reason "INFRA-123 block pdb in finance" /tmp/policy.yaml
```

</div>

---

### `admin rollback --reason TEXT [--to VERSION]`

یک نسخه‌ی قبلی را دوباره فعال می‌کند (اتمیک).

| گزینه | مقدار | توضیح |
|---|---|---|
| `--reason` | **الزامی** | دلیل |
| `--to` | `3` یا `000003` | نسخه‌ی مقصد؛ پیش‌فرض `PREVIOUS` |

نسخه‌ی مقصد قبل از فعال‌سازی بررسی می‌شود (checksum). رویداد audit: `POLICY_ROLLBACK`.

---

### `admin versions`

فهرست نسخه‌های ذخیره‌شده: `*` برای نسخه‌ی فعال و `p` برای نسخه‌ی قبلی، به‌همراه نام، revision، زمان و فرد مستقرکننده.

---

### `admin apply-membership [--force] <membership.json | ->`

cache عضویت را به‌صورت اتمیک نصب می‌کند و نسخه‌ی فعلی را در `previous.json` نگه می‌دارد.

| گزینه | توضیح |
|---|---|
| `--force` | عبور از محافظ‌ها (کاهش بیش از ۳۰٪، گروه جاافتاده، داده‌ی کهنه). فقط بعد از بررسی GitLab |

همیشه رد می‌شود: فایل خراب، schema نادرست، و `generated_at` در آینده (حتی با `--force`). رویدادهای audit: `MEMBERSHIP_SYNC` یا `MEMBERSHIP_SYNC_REFUSED`.

---

### `admin prune-logs [--days N]`

فایل‌های audit قدیمی‌تر از مدت نگهداری را حذف می‌کند.

| گزینه | مقدار | پیش‌فرض |
|---|---|---|
| `--days` | عدد | `settings.audit.retention_days` از policy فعال (پیش‌فرض ۱۸۰) |

فایل امروز و `break-glass.log` هرگز حذف نمی‌شوند. رویداد audit: `LOGS_PRUNED`.

---

## ۴. break-glass (راه اضطراری، بدون دستور)

فقط با دسترسی root روی سرور:

<div dir="ltr">

```bash
docker exec -u root gitlab touch /var/opt/gitlab/git-policy/state/break-glass   # فعال: همه‌ی pushها از git-policy عبور می‌کنند
docker exec -u root gitlab rm    /var/opt/gitlab/git-policy/state/break-glass   # غیرفعال
docker exec gitlab cat /var/opt/gitlab/git-policy/logs/break-glass.log          # pushهایی که در این مدت عبور کردند
```

</div>

تا وقتی فایل break-glass وجود دارد، `status` وضعیت **CRITICAL** نشان می‌دهد.

---

## ۵. `git-policy-ctl`: کانال Jenkins

Jenkins هیچ دسترسی مستقیمی به docker یا shell سرور ندارد. فقط با SSH به کاربر `gitpolicy-deploy` وصل می‌شود، و SSH برای این کاربر **فقط** همین برنامه را اجرا می‌کند (forced command). دستور درخواستی Jenkins از طریق `SSH_ORIGINAL_COMMAND` می‌رسد و **هرگز به shell داده نمی‌شود**.

### نصب روی Docker host واقعی (فاز ۱۱ کامل می‌شود)

<div dir="ltr">

```
/usr/local/sbin/git-policy-ctl                     root:root 0755
~gitpolicy-deploy/.ssh/authorized_keys:
  command="sudo -n /usr/local/sbin/git-policy-ctl",restrict ssh-ed25519 AAAA... jenkins
/etc/sudoers.d/git-policy (0440):
  Defaults!/usr/local/sbin/git-policy-ctl env_keep += "SSH_ORIGINAL_COMMAND SSH_CLIENT"
  gitpolicy-deploy ALL=(root) NOPASSWD: /usr/local/sbin/git-policy-ctl
```

</div>

> خط `env_keep` **ضروری** است. بدون آن، sudo متغیر `SSH_ORIGINAL_COMMAND` را پاک می‌کند و هر دستوری فقط راهنما را چاپ می‌کند. این باگ در lab پیدا و رفع شد.

### قواعد ورودی

- شکل همه‌ی گزینه‌ها `--نام=مقدار` است و هر مقدار با یک الگوی سخت‌گیرانه بررسی می‌شود.
- متن آزاد (`actor` و `reason`) **base64** فرستاده می‌شود: `--actor-b64=…` و `--reason-b64=…`. به این ترتیب هیچ کاراکتری نمی‌تواند از نقل‌قول فرار کند.
- داده‌ی بزرگ (policy، فایل عضویت، باینری) از **stdin** می‌آید و اندازه‌اش محدود است: policy ۴ MiB، عضویت ۶۴ MiB، باینری ۱۲۸ MiB.
- هر فراخوانی در `/var/log/git-policy-ctl.log` روی host ثبت می‌شود.

### فعل‌ها (verbs)

| فعل | گزینه‌ها | ورودی/خروجی | معادل |
|---|---|---|---|
| `status` | `[--json]` | — | `status` |
| `versions` | — | — | `admin versions` |
| `validate` | — | policy روی stdin | `validate --json -` |
| `explain` | `--project=P` (الزامی)، `[--user=U] [--ref=R] [--groups=a,b]` | JSON | `explain --json` |
| `apply` | `--actor-b64 --reason-b64` | policy روی stdin | `admin apply -` |
| `rollback` | `--actor-b64 --reason-b64 [--to=N]` | — | `admin rollback` |
| `enable` | `--actor-b64 --reason-b64` | — | `admin enable` |
| `disable` | `--actor-b64 --reason-b64 --ttl=2h` | — | `admin disable` |
| `groups` | — | فهرست گروه‌ها | `groups` |
| `show-policy` | `[--version=N]` | policy.yaml ذخیره‌شده | `show-policy` |
| `apply-membership` | `--actor-b64 [--force]` | JSON عضویت روی stdin | `admin apply-membership -` |
| `retire-hook` | `--actor-b64 --name=FILE` | — | `admin retire-hook` |
| `deploy` | `--actor-b64 --sha256=HEX` | باینری روی stdin | کپی به کانتینر، بررسی sha256 دو بار، سپس `admin install` |
| `prune-logs` | `--actor-b64 [--days=N]` | — | `admin prune-logs` |
| `backup` | — | tar.gz روی stdout (policies، state، membership) | — |
| `logs` | `--date=YYYY-MM-DD` | JSONL روی stdout | — |

### مثال‌ها (همان کاری که Jenkins انجام می‌دهد)

<div dir="ltr">

```bash
b64() { printf '%s' "$1" | base64 -w0; }
SSH="ssh -i jenkins_key gitpolicy-deploy@gitlab-host"

$SSH status
$SSH "explain --user=alex --project=finance/payment-api"
$SSH "apply --actor-b64=$(b64 'jenkins#57') --reason-b64=$(b64 'INFRA-123')" < policy.yaml
$SSH "disable --actor-b64=$(b64 'jenkins#58') --reason-b64=$(b64 'incident INC-9') --ttl=1h"
$SSH "deploy --actor-b64=$(b64 'jenkins#60') --sha256=$(sha256sum git-policy | cut -d' ' -f1)" < git-policy
$SSH backup > git-policy-backup.tgz
$SSH "logs --date=2026-09-29" > audit.jsonl
```

</div>

دستورهایی که **رد می‌شوند** (در lab تست شده):
- `sh`، `status;id`، `status$(id)`: فعل مجاز نیست
- `status --evil=1`: گزینه‌ی غیرمنتظره
- `--ttl=1d`: الگو نادرست
- `--name=../../etc/passwd`: الگو نادرست
- متن با newline: کاراکتر کنترلی
- هر دستور دلخواه مثل `docker ps`: forced command

---

## ۶. `install.sh` و `uninstall.sh` (روی Docker host)

### `install.sh`

| گزینه | پیش‌فرض | توضیح |
|---|---|---|
| `--container NAME` | `gitlab` | نام کانتینر GitLab |
| `--binary PATH` | جدیدترین `dist/git-policy-*-linux-amd64` | باینری برای نصب |
| `--actor NAME` | `install.sh:$USER` | در audit log |
| `--replace-hook` | — | جایگزینی hook متفاوت (با backup) |
| `--dry-run` | — | فقط بررسی‌ها، بدون هیچ تغییری |

بررسی‌ها:
1. sha256 باینری
2. در حال اجرا بودن کانتینر
3. mount بودن `/var/opt/gitlab`
4. وجود کاربر `git`
5. تنظیم `custom_hooks_dir` در Gitaly
6. hookهای موجود

بعد از نصب، `status` نمایش داده می‌شود.

### `uninstall.sh`

| گزینه | پیش‌فرض | توضیح |
|---|---|---|
| `--container NAME` | `gitlab` | |
| `--actor NAME` | `uninstall.sh:$USER` | |
| `--force` | — | حذف hook تغییر یافته |

فقط hook حذف می‌شود. حذف کامل داده‌ها عمداً دستی است (`./uninstall.sh --help`).

---

## ۶-الف. Jenkins (فاز ۱۱)

### `deploy/install-ctl.sh` (روی Docker host، با root)

| گزینه | پیش‌فرض | توضیح |
|---|---|---|
| `--pubkey FILE` | **الزامی** | کلید عمومی Jenkins |
| `--container NAME` | `gitlab` | نام کانتینر GitLab |
| `--dry-run` | — | فقط نمایش برنامه |

این اسکریپت:
- کاربر `gitpolicy-deploy` را می‌سازد: بدون رمز و **بدون** عضویت در گروه docker.
- `git-policy-ctl` را در `/usr/local/sbin` نصب می‌کند.
- فایل `/etc/sudoers.d/git-policy` را می‌سازد (بررسی‌شده با `visudo -c`).
- `authorized_keys` را با `command="sudo -n …git-policy-ctl",restrict` تنظیم می‌کند.

دوباره اجرا کردنش بی‌خطر است (idempotent).

### job `git-policy`: پارامترها

| پارامتر | مقدار | کاربرد |
|---|---|---|
| `ACTION` | `STATUS` · `VALIDATE_POLICY` · `UPDATE_POLICY` · `ENABLE` · `DISABLE` · `ROLLBACK_POLICY` · `BACKUP_POLICY` · `SYNC_GROUP_MEMBERSHIP` · `EXPLAIN` · `DEPLOY` · `RETIRE_HOOK` · `PRUNE_LOGS` · `EXPORT_LOGS` | کار مورد نظر |
| `ENVIRONMENT` | `TEST` · `PRODUCTION` | محیط |
| `REASON` | ۱۰ تا ۵۰۰ کاراکتر | **الزامی برای هر تغییر** |
| `POLICY_REF` | شاخه/تگ/commit | پیش‌فرض `main` |
| `DISABLE_TTL` | `30m`…`24h` | برای DISABLE |
| `ROLLBACK_TO` | عدد | خالی = قبلی |
| `FORCE` | true/false | برای sync؛ در PRODUCTION تأیید لازم دارد |
| `EXPLAIN_USER` · `EXPLAIN_PROJECT` · `EXPLAIN_REF` · `EXPLAIN_GROUPS` | — | برای EXPLAIN |
| `HOOK_NAME` | نام فایل | برای RETIRE_HOOK |
| `LOG_DATE` | `YYYY-MM-DD` | برای EXPORT_LOGS |

**تأیید در PRODUCTION:** اکشن‌های `UPDATE_POLICY`، `DISABLE`، `ROLLBACK_POLICY`، `DEPLOY`، `RETIRE_HOOK` و sync با `FORCE` منتظر تأیید یکی از `GP_APPROVERS` می‌مانند (حداکثر ۳۰ دقیقه). درخواست‌کننده نمی‌تواند خودش تأیید کند.

### متغیرهای سراسری Jenkins

| متغیر | نمونه (lab) |
|---|---|
| `GP_POLICY_REPO` | `http://gitlab/platform/git-policy-config.git` |
| `GP_POLICY_REPO_CRED` | `git-policy-config-read` |
| `GP_CTL_TEST` / `GP_CTL_PRODUCTION` | `gitpolicy-deploy@gp-ctl` |
| `GP_SSH_CRED_TEST` / `GP_SSH_CRED_PRODUCTION` | `git-policy-ssh-test` / `git-policy-ssh-prod` |
| `GP_GITLAB_URL_TEST` / `GP_GITLAB_URL_PRODUCTION` | `http://gitlab` |
| `GP_GITLAB_TOKEN_TEST` / `GP_GITLAB_TOKEN_PRODUCTION` | `gitlab-api-readonly` |
| `GP_APPROVERS` | `approver` (کاربر یا گروه Jenkins، جداشده با کاما) |
| `GP_BIN` | `/opt/git-policy/git-policy-0.1.0-linux-amd64` |
| `GP_SSH_KNOWN_HOSTS` (اختیاری) | فایل known_hosts برای pin کردن host key؛ **در production الزامی شود** |

### job ‌های دیگر

| job | کار |
|---|---|
| `git-policy-gitops` | هر ۲ دقیقه repo policy را بررسی می‌کند: `test/policy.yaml` را validate و روی TEST اعمال می‌کند، و برای PRODUCTION فقط drift را گزارش می‌دهد (UNSTABLE) |
| `git-policy-sync-membership` | همگام‌سازی گروه‌ها هر ۱۵ دقیقه (پارامتر `FORCE`) |
| `git-policy-status` | وضعیت سریع (lab) |

---

## ۷. lab محلی (`lab/`)

| دستور | کار |
|---|---|
| `lab/up.sh` | بالا آوردن GitLab CE 17.10.5 + Jenkins + gp-ctl؛ رمزها و کلیدها را یک بار می‌سازد |
| `lab/down.sh` | توقف (داده‌ها حفظ می‌شوند) |
| `lab/down.sh --purge` | توقف و حذف همه‌ی داده‌ها |
| `lab/seed-policy-repo.sh` | ساخت repo GitOps یعنی `platform/git-policy-config` با محتوای اولیه (`lab/policy-repo-seed/`)؛ `up.sh` خودش آن را اجرا می‌کند |

| سرویس | آدرس | کاربر | رمز |
|---|---|---|---|
| GitLab | http://localhost:8080 (ssh: پورت 2222) | `root` | `lab/.env` → `GITLAB_ROOT_PASSWORD` |
| Jenkins | http://localhost:8081 | `admin` (و `approver`) | `lab/.env` → `JENKINS_ADMIN_PASSWORD` |
| gp-ctl | فقط از داخل شبکه‌ی Docker | `gitpolicy-deploy` (کلید SSH) | `lab/ctl/jenkins_ctl_key` |

- **Jenkins:** credentialهای `git-policy-ssh-test` (کلید SSH) و `gitlab-api-readonly` (token گیت‌لب) از قبل تعریف شده‌اند.
- **Jobها:**
  - `git-policy-status`: وضعیت از مسیر Jenkins → gp-ctl → GitLab.
  - `git-policy-sync-membership`: همگام‌سازی گروه‌ها هر ۱۵ دقیقه، با پارامتر `FORCE`.
  - pipeline کامل در فاز ۱۱.
- pipelineها در `lab/jenkins/pipelines/*.groovy` هستند. باینری از `dist/` در `/opt/git-policy` داخل Jenkins mount می‌شود.
- فایل‌های رمز در git **نیستند** (`.gitignore`).

---

## ۸. build و تست

| دستور | کار |
|---|---|
| `tools/build.sh vendor` | دانلود وابستگی‌ها (یک بار، نیاز به اینترنت) |
| `tools/build.sh test` | gofmt + go vet + تست‌های واحد |
| `tools/build.sh build` | ساخت `dist/git-policy-<version>-linux-amd64` + sha256 |
| `tools/build.sh all` | test + build |
| `tests/integration/phase4.sh` … `phase8.sh` | تست با `git push` واقعی روی repo محلی (۳۷/۴۰/۲۳/۴۲/۲۲ مورد) |
| `tests/examples/verify.sh ["" 01 02 …]` | همه‌ی ردیف‌های ۱۰ مثال `EXAMPLES-FA.md` (۶۶ push) |
| `tests/gitlab/verify-jenkins.sh` | **فاز ۱۱:** همه‌ی اکشن‌های Jenkins، GitOps، تأیید چهار چشم، عدم نشت secret |
| `tests/gitlab/verify-gitlab.sh` | **همه‌ی فازها روی GitLab واقعی lab** (۸۵ بررسی): HTTP، SSH، Web، fork+MR، wiki، deploy key، کانال و jobهای Jenkins، sync گروه‌ها، TEST 17/18 |
| `tools/dev/check_schema.py` | بررسی JSON Schema با فایل‌های نمونه |
| `tools/dev/rtl.py FILE…` | راست‌چین کردن اسناد فارسی (کد بلاک‌ها چپ‌چین) |

متغیر محیطی `GO_IMAGE` در `tools/build.sh` image مورد استفاده را عوض می‌کند (پیش‌فرض `git-policy-build:go1.24`).

---

## ۹. فایل‌ها روی سرور (مرجع سریع)

| مسیر | محتوا |
|---|---|
| `…/gitaly/custom_hooks/pre-receive.d/50-git-policy` | اسکریپت hook (~۱۰ خط) |
| `/var/opt/gitlab/git-policy/bin/git-policy` | باینری (+ `.previous`) |
| `…/policies/ACTIVE`، `PREVIOUS`، `00000N/` | نسخه‌های policy (تغییرناپذیر) |
| `…/state/engine.json` | روشن/خاموش، دلیل، زمان انقضا |
| `…/state/break-glass` | (عادی: وجود ندارد) |
| `…/membership/current.json`، `previous.json` | cache عضویت گروه‌ها |
| `…/logs/audit-YYYY-MM-DD.jsonl` | audit log روزانه |
| `…/logs/break-glass.log` | pushهای عبورکرده در حالت اضطراری |
| `…/backup/` | backup hookها و فایل‌های جایگزین‌شده |

</div>
