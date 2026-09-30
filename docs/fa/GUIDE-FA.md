<div dir="rtl">

# راهنمای فارسی git-policy

> نسخه نرم‌افزار: **v0.1.0** — نسخه schema سیاست: **`git-policy/v1`**
> وضعیت: فازهای ۱ تا ۱۱ انجام شده و **همه روی GitLab واقعی (17.10.5) تست شده‌اند** (۸۵ از ۸۵). همه‌ی قوانین enforcement فعال‌اند و روی **همه‌ی commitهای جدید** هر push اجرا می‌شوند. audit log کامل است.
>
> 🚀 **نصب روی سرور واقعی، قدم به قدم:** [DEPLOY-FA.md](DEPLOY-FA.md)
>
> ✍️ **راهنمای نوشتن policy، قدم به قدم (همه‌ی کلیدها و گزینه‌ها):** [POLICY-FA.md](POLICY-FA.md)
>
> 📘 **راهنمای کامل همه‌ی دستورات و گزینه‌ها:** [CLI-FA.md](CLI-FA.md)  — 📗 **۱۰ مثال:** [EXAMPLES-FA.md](EXAMPLES-FA.md)
>
> **رابط کاربری نهایی این سیستم Jenkins است.** دستورهای `docker exec ... admin` در این راهنما فقط برای تست lab در همین مراحل‌اند؛ در فاز ۱۱ همه‌ی ورودی‌ها و عملیات از طریق Jenkins انجام می‌شود.

---

## ۱. git-policy چیست؟

یک لایه‌ی **اجرای سیاست سازمانی روی سرور Git** برای GitLab Self-Managed است که از طریق **Global Pre-Receive Hook** در Gitaly اجرا می‌شود.
هر `git push` قبل از ثبت، توسط این موتور بررسی می‌شود و یا **پذیرفته** یا **رد** می‌شود.

<div dir="ltr">

```
Developer ── git push ──> GitLab ──> Gitaly ──> pre-receive.d/50-git-policy (اسکریپت کوچک)
                                                        │
                                                        ▼
                                          git-policy hook (باینری Go)
                                                        │
                        ┌───────────────┬───────────────┴──────────────┐
                        ▼               ▼                              ▼
                  وضعیت موتور     سیاست فعال (ACTIVE)          Audit Log
                  (روشن/خاموش)    (نسخه‌ی تغییرناپذیر)          (JSON Lines)

Jenkins (Control Plane) ──> فقط مدیریت: apply / enable / disable / rollback / status
```

</div>

**اصول کلیدی:**

| اصل | توضیح |
|---|---|
| Hook خیلی کوچک | فایل hook فقط ~۱۰ خط است و هیچ منطقی ندارد؛ همه‌چیز در باینری است |
| Jenkins فقط مدیریت می‌کند | منطق تصمیم‌گیری در Jenkins نیست؛ اگر Jenkins از کار بیفتد، enforcement ادامه دارد |
| هیچ وابستگی شبکه‌ای در مسیر push | نه GitLab API، نه Nexus، نه Jenkins — همه‌چیز local |
| تغییر اتمیک | سیاست ناقص یا نیمه‌نوشته هرگز خوانده نمی‌شود |
| Fail-closed برای موارد امنیتی | اگر سیاست قابل خواندن نباشد، push رد می‌شود (نه اینکه بی‌صدا رد شود) |
| جایگزین GitLab نیست | Protected Branches، MR approvals و نقش‌ها همچنان کار GitLab است |

---

## ۲. وضعیت پروژه (فازها)

| فاز | موضوع | وضعیت |
|---|---|---|
| ۱ | معماری، ریسک‌ها، مدل خطا | ✅ تأیید شده |
| ۲ | Schema سیاست و قواعد اولویت | ✅ تأیید شده |
| ۳ | اسکلت Go، validator، compiler | ✅ تأیید شده |
| ۴ | ENABLE / DISABLE / STATUS، apply/rollback اتمیک، نصب | ✅ تأیید شده |
| ۵ | قوانین هویت و scope (user/group/namespace/project/ref)، استثناها، explain | ✅ تأیید شده |
| ۶ | پیمایش ضد-دور‌زدن (همه‌ی commitهای جدید)، محدودیت‌ها، timeout | ✅ تأیید شده |
| ۷ | پسوند و مسیر فایل (DLL, EXE, ...)، حالت audit، بازنشسته کردن PoC | ✅ تأیید شده |
| ۸ | حجم blob و امضای PE | ✅ تأیید شده |
| ۹ | audit کامل: `log_accepted`، `required`، retention، فیلدهای SIEM | ✅ تأیید شده |
| ۱۰ | همگام‌سازی عضویت گروه‌ها از GitLab با Jenkins، محافظ ایمنی، هشدار W010 | ✅ تأیید شده |
| ۱۱ | Jenkins با GitOps: job اصلی با ۱۳ اکشن، تأیید چهار چشم، job ‌GitOps، نصب کانال روی host | ✅ **انجام شده (منتظر تأیید)** |
| lab | GitLab 17.10.5 + Jenkins + gp-ctl با docker-compose؛ تست همه‌ی فازها روی GitLab واقعی | ✅ **۸۵ از ۸۵** |
| ۱۲ به بعد | تست جامع، سخت‌سازی امنیتی، کارایی، مستندات تولید | ⏳ |

> ✅ از فاز ۷ به بعد **git-policy خودش DLL/EXE و مسیرهای ممنوع را رد می‌کند.** PoC قدیمی (`01-block-dll`) تا زمانی که شما روی lab تأیید کنید دست‌نخورده می‌ماند و بعد با `admin retire-hook` بازنشسته می‌شود (بخش ۱۵.۵).

---

## ۳. ساختار پروژه

<div dir="ltr">

```
~/infra/git-policy/
├── cmd/git-policy/          ورودی CLI
├── internal/
│   ├── policy/              خواندن سخت‌گیرانه‌ی YAML، validator (کدهای V/W)، compiler
│   ├── hook/                منطق زمان push (pre-receive)
│   ├── engine/              تصمیم هویت، استثناها، قوانین محتوایی مؤثر، کلاس سیاست
│   ├── membership/          cache محلی عضویت گروه‌ها
│   ├── gitscan/             پیمایش ضد-دور‌زدن (فقط git plumbing)
│   ├── store/               نسخه‌های سیاست، ACTIVE/PREVIOUS، apply/rollback اتمیک
│   ├── state/               سوییچ روشن/خاموش موتور
│   ├── audit/               لاگ ممیزی JSON Lines
│   ├── admin/               install/enable/disable/apply/rollback/status + فایل hook
│   ├── message/             پیام‌های GL-HOOK-ERR (با پاک‌سازی کاراکترهای خطرناک)
│   ├── fsutil/              نوشتن اتمیک، قفل، خواندن بدون دنبال‌کردن symlink
│   └── layout/              مسیرها و مجوزهای روی سرور
├── examples/                نمونه سیاست‌ها
├── schema/                  JSON Schema (برای ادیتور)
├── testdata/policies/       فایل‌های نامعتبر برای تست (با کد خطای مورد انتظار)
├── tests/integration/       تست با git push واقعی
├── tools/build.sh           build و test داخل Docker (نیازی به نصب Go نیست)
├── install.sh / uninstall.sh  نصب روی کانتینر GitLab (روی Docker host اجرا می‌شود)
└── docs/                    مستندات طراحی + همین راهنما
```

</div>

---

## ۴. ساخت (Build) و تست

نیازی به نصب Go ندارید؛ همه‌چیز داخل کانتینر (`golang:1.24-alpine` به‌علاوه‌ی git) اجرا می‌شود.

<div dir="ltr">

```bash
cd ~/infra/git-policy

tools/build.sh test      # gofmt + go vet + تست‌های واحد
tools/build.sh build     # ساخت dist/git-policy-0.1.0-linux-amd64 + فایل sha256
tools/build.sh all       # هر دو

tests/integration/phase4.sh   # ۳۷ تست: نصب، روشن/خاموش، apply/rollback، fail-closed
tests/integration/phase5.sh   # ۴۰ تست: قوانین کاربر/گروه/استثنا با git push واقعی
tests/integration/phase6.sh   # ۲۳ تست: پیمایش ضد-دور‌زدن داخل quarantine گیت، محدودیت‌ها، کارایی
tests/integration/phase7.sh   # ۴۲ تست: رد DLL/EXE/مسیر با hook واقعی، ترفندهای NTFS، حالت audit، هم‌زمانی
tests/integration/phase8.sh   # ۲۲ تست: حجم فایل، سقف mandatory و استثنا، امضای PE، کارایی
```

</div>

خروجی مورد انتظار:

<div dir="ltr">

```
RESULT: 37 passed, 0 failed     (phase4)
RESULT: 40 passed, 0 failed     (phase5)
RESULT: 23 passed, 0 failed     (phase6)
RESULT: 42 passed, 0 failed     (phase7)
RESULT: 22 passed, 0 failed     (phase8)
```

</div>

اولین اجرای `tools/build.sh` یک image کوچک به نام `git-policy-build:go1.24` (Go + git) می‌سازد (فقط یک بار، نیاز به اینترنت).

باینری خروجی **static** است (حدود ۳ مگابایت) و روی هر لینوکس amd64 بدون هیچ وابستگی اجرا می‌شود.

---

## ۵. نوشتن سیاست (Policy)

> راهنمای کامل و قدم به قدم نوشتن policy، با همه‌ی کلیدها، مقدارهای مجاز، قواعد اولویت و کدهای خطا: **[POLICY-FA.md](POLICY-FA.md)**. این بخش فقط خلاصه است.

### ۵.۱ ساختار کلی

<div dir="ltr">

```yaml
apiVersion: git-policy/v1        # نسخه schema (ربطی به نسخه نرم‌افزار ندارد)
kind: GitPolicy
metadata:
  name: org-git-policy
  revision: 1                    # با هر تغییر باید افزایش یابد
settings:   { ... }              # حالت، محدودیت‌ها، پیام‌ها
mandatory:  { ... }              # کف امنیتی — هیچ سطح پایین‌تری نمی‌تواند آن را تضعیف کند
defaults:   { ... }              # پیش‌فرض سازمانی، قابل override
namespaces: { finance: {...} }   # قوانین گروه/زیرگروه
projects:   { finance/payment-api: {...} }
users:      { alex: {...} }
groups:     { contractors: {...} }
exceptions: [ ... ]              # استثناهای صریح، محدود و تاریخ‌دار
```

</div>

نمونه‌ی کامل: `examples/policy.example.yaml`
**۱۰ مثال از ساده تا کامل (با توضیح فارسی و تست‌شده):** [`docs/fa/EXAMPLES-FA.md`](EXAMPLES-FA.md)
مرجع دقیق: `docs/design/PHASE-2-SCHEMA.md`

### ۵.۲ سه نوع قانون

| نوع | چه کسی می‌تواند آن را تضعیف کند؟ | مثال |
|---|---|---|
| **mandatory** | فقط یک exception با `mandatory: true` | DLL و EXE ممنوع، سقف ۵۰MiB |
| **defaults / scoped** | سطح دقیق‌تر (namespace → project → ref) | namespace مالی `.pdb` را هم ممنوع کند |
| **exceptions** | خودشان استثنا هستند | اجازه‌ی موقت یک DLL خاص تا تاریخ مشخص |

### ۵.۳ قواعد اولویت (خلاصه)

- **لیست پسوندها و مسیرها:** همه‌ی سطوح با هم **جمع** می‌شوند. سطح پایین‌تر فقط با `unblock_extensions` صریح می‌تواند چیزی را حذف کند — و هرگز چیزی که در mandatory است.
- **حجم فایل:** دقیق‌ترین سطح برنده است، ولی هرگز بیشتر از سقف mandatory.
- **کاربر/گروه:** دقیق‌ترین سطح (project > namespace > global) تصمیم می‌گیرد؛ در یک سطح، قانون **کاربر** بر **گروه** مقدم است؛ در تساوی، **deny** برنده است.
- **cache گروه کهنه:** فقط می‌تواند **محدود** کند، هرگز اجازه نمی‌دهد (allowهای گروهی نادیده گرفته می‌شوند، denyها باقی می‌مانند).

### ۵.۴ نکات نوشتاری

- حجم با واحد باینری: `20MiB`، `512KiB`، `1GiB` (واحد `MB` قبول **نمی‌شود**).
- الگوی ref کامل: `refs/heads/release/**` (نه `release/*`).
- `*` داخل یک بخش مسیر، `**` چند بخش؛ `**/*.dll` یعنی هر عمقی.
- کلیدهای ناشناخته **خطا** هستند (غلط املایی بی‌صدا نادیده گرفته نمی‌شود).
- کلیدهایی مثل `bypass_all`، `bypass_content_policy`، `direct_push` عمداً **وجود ندارند**.

### ۵.۵ اعتبارسنجی قبل از استقرار

<div dir="ltr">

```bash
B=dist/git-policy-0.1.0-linux-amd64
$B validate examples/policy.example.yaml          # خروجی متنی
$B validate --json examples/policy.example.yaml   # برای Jenkins
$B compile examples/policy.example.yaml           # دیدن نسخه‌ی نرمال‌شده (compiled.json)
```

</div>

کد خروج: `0` معتبر، `1` نامعتبر، `2` خطای ورودی/فایل.
نمونه‌ی خطا:

<div dir="ltr">

```
ERROR   V020  projects["finance/legacy-erp"].unblock_extensions: "dll" is blocked by mandatory and cannot be unblocked ...
RESULT: INVALID (1 errors, 0 warnings)
```

</div>

---

## ۶. نصب روی Lab

### ۶.۱ پیش از نصب (مهم)

- نصب **هیچ سرویسی را restart نمی‌کند** (نه GitLab، نه Gitaly، نه Docker).
- hook قدیمی `01-block-dll` **دست‌نخورده** می‌ماند.
- موتور در اولین نصب **خاموش (DISABLED)** است؛ یعنی نصب هیچ تغییری در رفتار push ایجاد نمی‌کند.
- اگر فایلی با نام `50-git-policy` از قبل با محتوای متفاوت وجود داشته باشد، نصب **متوقف** می‌شود (مگر با `--replace-hook` که اول backup می‌گیرد).

### ۶.۲ انتقال فایل‌ها به Docker host

<div dir="ltr">

```bash
# روی همین workstation:
cd ~/infra/git-policy
tools/build.sh all
scp -r dist install.sh uninstall.sh examples <user>@192.168.120.128:~/git-policy/
```

</div>

### ۶.۳ نصب (روی Docker host lab)

<div dir="ltr">

```bash
cd ~/git-policy

# ۱) فقط بررسی‌ها، بدون هیچ تغییری:
./install.sh --dry-run

# ۲) نصب واقعی:
./install.sh --actor "soroush"
```

</div>

بررسی‌هایی که `install.sh` انجام می‌دهد:
1. وجود باینری و تطابق sha256
2. در حال اجرا بودن کانتینر `gitlab`
3. mount بودن `/var/opt/gitlab` (پایدار بودن داده‌ها)
4. وجود کاربر `git`
5. وجود `custom_hooks_dir` در تنظیمات Gitaly و پوشه‌ی `pre-receive.d`
6. نمایش hookهای موجود
7. کپی باینری، بررسی مجدد sha256 داخل کانتینر، اجرای `admin install`، و نمایش `status`

خروجی مورد انتظار در انتها:

<div dir="ltr">

```
HEALTH: WARNING
  - WARNING: engine is disabled without expiry (initial install state)
  - WARNING: no loadable policy: no policy has been deployed
installed. Next: deploy a policy (admin apply) and enable it (admin enable).
```

</div>

(WARNING در این مرحله طبیعی است.)

### ۶.۴ استقرار سیاست و روشن کردن

<div dir="ltr">

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"

docker cp examples/policy.minimal.yaml gitlab:/tmp/policy.yaml
$GP admin apply   --actor soroush --reason "first policy" /tmp/policy.yaml
$GP admin enable  --actor soroush --reason "lab test"
$GP status
```

</div>

---

## ۷. عملیات روزمره

همه‌ی دستورات مدیریتی باید به صورت **root** داخل کانتینر اجرا شوند (`docker exec -u root`). در ادامه:

<div dir="ltr">

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"
```

</div>

| کار | دستور |
|---|---|
| وضعیت | `$GP status` یا `$GP status --json` |
| استقرار سیاست جدید | `docker cp policy.yaml gitlab:/tmp/p.yaml && $GP admin apply --actor NAME --reason "..." /tmp/p.yaml` |
| روشن کردن | `$GP admin enable --actor NAME --reason "..."` |
| خاموش کردن موقت | `$GP admin disable --actor NAME --reason "..." --ttl 2h` |
| بازگشت به نسخه‌ی قبل | `$GP admin rollback --actor NAME --reason "..."` |
| بازگشت به نسخه‌ی مشخص | `$GP admin rollback --actor NAME --reason "..." --to 3` |
| فهرست نسخه‌ها | `$GP admin versions` |
| دیدن لاگ امروز | `docker exec gitlab sh -c 'cat /var/opt/gitlab/git-policy/logs/audit-$(date -u +%F).jsonl'` |

### ۷.۱ رفتار هر دستور

- **apply:** سیاست را validate می‌کند؛ اگر **نامعتبر** باشد یا `revision` آن از نسخه‌ی فعال **بزرگ‌تر نباشد** (خطای `V040`)، هیچ تغییری ایجاد نمی‌شود و سیاست قبلی فعال می‌ماند. در غیر این صورت یک پوشه‌ی نسخه‌ی جدید (`000002`، …) می‌سازد و با یک rename اتمیک فعالش می‌کند.
- **enable:** اگر هیچ سیاست سالمی موجود نباشد **رد می‌شود** (چون موتور روشن بدون سیاست، همه‌ی pushها را رد می‌کند).
- **disable:** حتماً `--reason` و `--ttl` لازم دارد (حداکثر ۱۶۸ ساعت). بعد از پایان زمان، موتور **خودکار** دوباره فعال می‌شود — خاموش‌ماندن فراموش‌شده ممکن نیست.
- **rollback:** حتماً `--reason` لازم دارد؛ نسخه‌ی مقصد قبل از فعال‌سازی بررسی می‌شود.
- همه‌ی این عملیات در **audit log** ثبت می‌شوند (چه کسی، کی، چرا).

### ۷.۲ کدهای خروج status

| کد | معنی |
|---|---|
| `0` | OK |
| `1` | WARNING (مثلاً موتور خاموش است) |
| `3` | CRITICAL (مثلاً hook نصب نیست، break-glass فعال است، یا موتور روشن است ولی سیاستی ندارد) |

نمونه خروجی:

<div dir="ltr">

```
git-policy v0.1.0  (root /var/opt/gitlab/git-policy)
  Engine:            enabled
    changed:         2026-09-29T10:24:16Z by jenkins#1
    reason:          go live
  Break-glass:       absent
  Hook wrapper:      installed (/var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/50-git-policy)
    other hooks:     01-block-dll
  Active policy:     000001  org-git-policy revision 1
  Policy checksum:   sha256:8358935b...
  Last deployment:   2026-09-29T10:24:16Z by jenkins#1
  Previous version:  -
  Stored versions:   1
  Membership cache:  not configured (arrives in Phase 10)
  Audit log:         audit-2026-09-29.jsonl (1180 bytes)
HEALTH: OK
```

</div>

---

## ۸. ساختار روی سرور و مجوزها

<div dir="ltr">

```
/var/opt/gitlab/git-policy/          root:git  0750
├── bin/git-policy                   root:root 0755   (+ git-policy.previous بعد از ارتقا)
├── policies/                        root:git  0750
│   ├── ACTIVE                       "000002"  — نسخه‌ی فعال
│   ├── PREVIOUS                     "000001"  — نسخه‌ی قبلی (برای rollback و fallback)
│   ├── 000001/                      0550 — تغییرناپذیر
│   │   ├── policy.yaml              0440 — متن اصلی تأییدشده
│   │   ├── compiled.json            0440 — نسخه‌ای که hook می‌خواند
│   │   └── meta.json                0440 — چه کسی، کی، checksum
│   └── 000002/ ...
├── state/                           root:git  0750
│   ├── engine.json                  روشن/خاموش + دلیل + زمان انقضا
│   └── break-glass                  (به طور عادی وجود ندارد)
├── logs/                            git:git   0750
│   ├── audit-2026-09-29.jsonl       یک فایل در روز (بدون مشکل rotation)
│   └── break-glass.log
├── backup/                          root:root 0700
└── tmp/                             root:root 0700

/var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/50-git-policy   root:root 0755
```

</div>

**نکته امنیتی:** کاربر `git` (که Gitaly و hook با آن اجرا می‌شوند) فقط **خواندن** سیاست/وضعیت و **افزودن** به لاگ را دارد. این موارد در تست بررسی شد و همه **رد** شدند:
تغییر state، ساختن break-glass، تغییر ACTIVE، حذف باینری، تغییر hook، ساختن نسخه‌ی جعلی.

---

## ۹. رفتار در شرایط خطا (Fail-open / Fail-closed)

| شرایط | رفتار | چرا |
|---|---|---|
| Jenkins در دسترس نیست | **بدون اثر** | enforcement کاملاً local است |
| GitLab API / Nexus در دسترس نیست | **بدون اثر** | در مسیر push هیچ فراخوانی شبکه‌ای نیست |
| فایل state حذف شده | موتور **روشن** در نظر گرفته می‌شود | حذف یک فایل نباید امنیت را خاموش کند |
| فایل state خراب | push **رد** می‌شود | fail-closed |
| نسخه‌ی ACTIVE خراب | از **PREVIOUS** استفاده می‌شود + ثبت در لاگ + WARNING در status | ادامه‌ی سرویس با آخرین نسخه‌ی سالم |
| هیچ سیاست سالمی نیست | push **رد** می‌شود (`POLICY_UNAVAILABLE`) | fail-closed |
| باینری حذف شده یا crash کند | push **رد** می‌شود | fail-closed |
| ورودی ref نامعتبر | push **رد** می‌شود (`INVALID_REF_UPDATE`) | fail-closed |
| نوشتن audit log شکست بخورد | تصمیم **تغییر نمی‌کند** (قابل تنظیم در آینده) | وابسته نکردن دسترس‌پذیری به لاگ |
| disable منقضی شده | موتور خودکار **روشن** می‌شود | خاموشی فراموش‌شده ممکن نیست |

پیامی که توسعه‌دهنده در این حالت‌ها می‌بیند:

<div dir="ltr">

```
remote: GL-HOOK-ERR: Push rejected by organizational Git policy.
remote: GL-HOOK-ERR: Rule: POLICY_UNAVAILABLE
remote: GL-HOOK-ERR: Required action: The Git policy service is unavailable. Contact the platform team.
```

</div>

جزئیات فنی (مسیر فایل، علت دقیق) **فقط در audit log** ثبت می‌شود، نه در پیام توسعه‌دهنده.

---

## ۱۰. شرایط اضطراری (Disaster Recovery)

### ۱۰.۱ سیاست جدید مشکل ایجاد کرده

<div dir="ltr">

```bash
$GP admin rollback --actor NAME --reason "rollback: rule X blocks valid pushes"
```

</div>

### ۱۰.۲ نیاز به توقف موقت موتور (موتور سالم است)

<div dir="ltr">

```bash
$GP admin disable --actor NAME --reason "incident INC-123" --ttl 1h
```

</div>

### ۱۰.۳ موتور کاملاً خراب است (باینری/state/سیاست) — Break-glass
این راه فقط با دسترسی root روی سرور ممکن است و **هر push** در حالت break-glass ثبت می‌شود:

<div dir="ltr">

```bash
# فعال‌سازی: همه‌ی pushها از git-policy عبور می‌کنند (hookهای دیگر مثل 01-block-dll همچنان اجرا می‌شوند)
docker exec -u root gitlab touch /var/opt/gitlab/git-policy/state/break-glass

# … رفع مشکل (install.sh دوباره، admin apply، admin rollback، …)

# غیرفعال‌سازی — فراموش نکنید! (status تا وقتی این فایل هست CRITICAL نشان می‌دهد)
docker exec -u root gitlab rm /var/opt/gitlab/git-policy/state/break-glass

# دیدن pushهایی که در این مدت عبور کرده‌اند:
docker exec gitlab cat /var/opt/gitlab/git-policy/logs/break-glass.log
```

</div>

### ۱۰.۴ فایل state خراب شده

<div dir="ltr">

```bash
$GP admin enable  --actor NAME --reason "repair state"      # یا:
$GP admin disable --actor NAME --reason "repair state" --ttl 1h
```

</div>

(هر دو فایل state را از نو و به صورت اتمیک می‌نویسند.)

### ۱۰.۵ نسخه‌های سیاست همه خراب شده‌اند
یک `admin apply` تازه همیشه ممکن است (حتی وقتی هیچ نسخه‌ای قابل خواندن نیست). `revision` را از آخرین مقدار بالاتر بگذارید.

### ۱۰.۶ hook تغییر کرده یا خراب شده

<div dir="ltr">

```bash
./install.sh --replace-hook     # نسخه‌ی فعلی در backup/ ذخیره می‌شود
```

</div>

### ۱۰.۷ حذف hook (بازگشت به وضعیت قبل)

<div dir="ltr">

```bash
./uninstall.sh
```

</div>

فقط `50-git-policy` حذف می‌شود (با backup)؛ سیاست‌ها، state، لاگ‌ها و باینری باقی می‌مانند و `install.sh` دوباره همه‌چیز را برمی‌گرداند.

---

## ۱۱. عیب‌یابی

| نشانه | بررسی | راه‌حل |
|---|---|---|
| همه‌ی pushها با `POLICY_UNAVAILABLE` رد می‌شوند | `$GP status` → بخش Engine و Active policy | `admin apply` یا `admin rollback`؛ یا موقتاً `admin disable --ttl` |
| status می‌گوید `Hook wrapper: MODIFIED` | کسی فایل hook را دستی تغییر داده | `./install.sh --replace-hook` |
| status می‌گوید `FALLBACK: ACTIVE unusable` | نسخه‌ی فعال خراب شده | `admin apply` نسخه‌ی جدید یا `admin rollback --to N` |
| `admin enable` رد می‌شود | هنوز سیاستی مستقر نشده | اول `admin apply` |
| `admin apply` با `V040` رد می‌شود | `revision` جدیدتر از نسخه‌ی فعال نیست | `metadata.revision` را افزایش دهید |
| `admin ...` با خطای «must run as root» | دستور بدون `-u root` اجرا شده | `docker exec -u root gitlab ...` |
| `another git-policy admin operation is in progress` | یک عملیات مدیریتی دیگر در حال اجراست | صبر کنید (حداکثر ۱۰ ثانیه انتظار خودکار) |
| پیام hook در ترمینال عجیب است (`\x1b`) | نام فایل/ref شامل کاراکتر کنترلی بوده | عمدی است: جلوگیری از تزریق به ترمینال |

---

## ۱۲. تست روی Lab (پیشنهادی برای تأیید فاز ۴)

<div dir="ltr">

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"

# ۱. نصب (موتور خاموش)
./install.sh --dry-run && ./install.sh --actor soroush
$GP status                                    # انتظار: HEALTH: WARNING (disabled)

# ۲. یک push معمولی از workstation → باید پذیرفته شود
#    و در لاگ یک رویداد PUSH_ALLOWED_ENGINE_DISABLED ثبت شود:
docker exec gitlab sh -c 'tail -n 3 /var/opt/gitlab/git-policy/logs/audit-*.jsonl'
#    این رویداد متغیرهای واقعی GL_USERNAME / GL_PROJECT_PATH / GL_PROTOCOL را نشان می‌دهد
#    (تأیید فرضیات فاز ۱ دربارهٔ محیط Gitaly).

# ۳. استقرار و روشن کردن
docker cp examples/policy.minimal.yaml gitlab:/tmp/policy.yaml
$GP admin apply  --actor soroush --reason "phase4 lab" /tmp/policy.yaml
$GP admin enable --actor soroush --reason "phase4 lab"
$GP status                                    # انتظار: HEALTH: OK

# ۴. push معمولی → پذیرفته؛ push با DLL → با پیام PoC رد می‌شود (01-block-dll)

# ۵. خاموش‌کردن موقت و بازگشت خودکار
$GP admin disable --actor soroush --reason "ttl test" --ttl 2m
$GP status                                    # disabled until ...
# بعد از ۲ دقیقه:
$GP status                                    # enabled (disable expired)

# ۶. پایان تست: خاموش گذاشتن موتور تا فاز بعد (اختیاری)
$GP admin disable --actor soroush --reason "waiting for phase 5" --ttl 168h
```

</div>

---

## ۱۳. فاز ۵: قوانین هویت (کاربر، گروه، پروژه)

### ۱۳.۱ چه چیزی اجرا می‌شود؟

برای **هر ref** در push (شاخه، تگ، حذف شاخه)، به این ترتیب:

<div dir="ltr">

```
۱. نوع repository (از GL_REPOSITORY):  project → همه‌ی قوانین
                                        wiki / snippet → فقط mandatory
                                        design → بررسی نمی‌شود
۲. policy قانون deny گروهی دارد ولی cache عضویت نیست؟      → ❌ MEMBERSHIP_UNAVAILABLE
۳. تعداد refها > settings.limits.max_ref_updates؟            → ❌ LIMIT_REF_UPDATES
۴. mandatory.deny_users / deny_groups                        → ❌ MANDATORY_USER/GROUP_DENIED
۵. قوانین users / groups (دقیق‌ترین سطح تصمیم می‌گیرد)        → ❌ USER/GROUP_PUSH_DENIED
۶. هر رد شدن اول با exceptions بررسی می‌شود؛ اگر استثنای معتبری بخورد → ✅ + ثبت EXCEPTION_APPLIED
```

</div>

- یک push چند-ref **همه یا هیچ** است: اگر حتی یک ref رد شود، هیچ refی ثبت نمی‌شود (رفتار خود Git).
- **حذف شاخه هم push است**؛ کاربری که `push: deny` دارد نمی‌تواند شاخه حذف کند.

### ۱۳.۲ مثال قوانین

<div dir="ltr">

```yaml
mandatory:
  deny_users: ["@unknown", terminated.user]   # @unknown = push بدون GL_USERNAME

users:
  alex:
    push: deny                                  # alex هیچ‌جا push نکند …
    projects:
      finance/reporting: {push: allow}          # … جز این پروژه (سطح پروژه دقیق‌تر است)
      finance/payment-api:
        refs: {"refs/heads/main": deny}         # فقط main در این پروژه

groups:
  contractors:
    push: deny
    namespaces:
      outsourcing: {push: allow}                # پیمانکاران فقط در outsourcing/*

exceptions:
  - id: EXC-HOTFIX-1
    rules: [USER_PUSH_DENIED]
    subjects: {users: [alex]}
    scope: {projects: [finance/payment-api], refs: ["refs/heads/hotfix/*"]}
    reason: incident INC-77 hotfix access
    expires: 2026-10-31
```

</div>

### ۱۳.۳ قواعد تصمیم (خلاصه)

| قاعده | مثال |
|---|---|
| **دقیق‌ترین سطح** برنده است: project+ref > project > namespace عمیق‌تر > namespace > global+ref > global | `alex: push: deny` + `projects.finance/reporting: allow` → در reporting مجاز |
| در یک سطح، **کاربر بر گروه** مقدم است | کاربر allow و گروهش deny در همان پروژه → مجاز |
| در تساوی، **deny** برنده است | عضو دو گروه با allow و deny در یک namespace → رد |
| **mandatory** بالاتر از همه است؛ فقط استثنای `mandatory: true` از آن عبور می‌کند | `terminated.user` همه‌جا رد |
| نام کاربر و مسیر پروژه **بدون حساسیت به بزرگی حروف** مقایسه می‌شوند | `ALEX` = `alex` |
| namespace بر اساس **بخش‌های مسیر** مقایسه می‌شود | `outsourcing` شامل `outsourcingx/app` نمی‌شود |

### ۱۳.۴ cache عضویت گروه‌ها

قوانین گروهی از فایل محلی `membership/current.json` استفاده می‌کنند؛ در مسیر push **هیچ تماسی با GitLab API گرفته نمی‌شود**. همگام‌سازی خودکار از GitLab توسط Jenkins در فاز ۱۰ ساخته می‌شود.

| سن cache | deny گروهی | allow گروهی و استثنای گروهی |
|---|---|---|
| تازه (≤ `soft_max_age`، پیش‌فرض ۲ ساعت) | اعمال | اعمال |
| کهنه (≤ `hard_max_age`، پیش‌فرض ۲۴ ساعت) | اعمال | اعمال + هشدار در status |
| منقضی (> ۲۴ ساعت) | اعمال (آخرین داده) | **نادیده**؛ cache کهنه فقط محدود می‌کند |
| وجود ندارد | اگر policy قانون deny گروهی دارد: **همه‌ی pushها رد** (`MEMBERSHIP_UNAVAILABLE`) | — |

**محافظ ایمنی:** اگر policy قانون deny گروهی داشته باشد و cache موجود نباشد، `admin enable` رد می‌شود. وقتی موتور روشن است، `admin apply` هم رد می‌شود (کد `V042`). این جلوی مسدود شدن تصادفی همه‌ی pushها را می‌گیرد.

### ۱۳.۵ پیامی که توسعه‌دهنده می‌بیند

<div dir="ltr">

```
remote: GL-HOOK-ERR: Push rejected by organizational Git policy.
remote: GL-HOOK-ERR: User: alex
remote: GL-HOOK-ERR: Project: finance/payment-api
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Rule: USER_PUSH_DENIED
remote: GL-HOOK-ERR: Ref: refs/heads/main
remote: GL-HOOK-ERR: Commit: 9447e1933e859021fe727872d480468dace5ed64
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Required action (USER_PUSH_DENIED): You are not permitted to push to this project or ref.
remote: GL-HOOK-ERR: Help: #devops-help
```

</div>

جزئیات داخلی (کدام خط policy باعث رد شد) در پیام **نیست** و فقط در audit log ثبت می‌شود:

<div dir="ltr">

```json
{"action":"REJECT","user":"alex","project":"finance/payment-api","ref":"refs/heads/main",
 "rule":"USER_PUSH_DENIED","source":"users[\"alex\"].projects[\"finance/payment-api\"].push",
 "commit":"9447e19...","policy_version":"000001","membership":"fresh","timestamp":"2026-09-29T14:25:55+03:30"}
```

</div>

### ۱۳.۶ دستور explain: «چرا رد شد؟»

بدون push واقعی نشان می‌دهد policy درباره‌ی یک push فرضی چه تصمیمی می‌گیرد. در فاز ۱۱ همین به شکل اکشن `EXPLAIN` در Jenkins درمی‌آید.

<div dir="ltr">

```bash
git-policy explain --policy examples/policy.example.yaml \
    --user carol --groups contractors --project outsourcing/portal
```

</div>

<div dir="ltr">

```
Identity
  => namespace(depth 1)       group allow groups["contractors"].namespaces["outsourcing"].push
     global                   group deny  groups["contractors"].push
Verdict:     ALLOW
Effective content rules (enforced from Phases 7-8)
  blocked extensions: 7z (defaults), dll (MANDATORY), exe (MANDATORY), ...
  max file size:      20MiB (defaults.max_file_size)
```

</div>

- `=>` قانونی است که تصمیم را گرفته.
- بدون `--policy`، سیاست فعال سرور و cache عضویت خوانده می‌شوند.
- بخش «Effective content rules» نشان می‌دهد در فازهای ۷ و ۸ برای این پروژه و شاخه چه چیزی ممنوع خواهد بود.

### ۱۳.۷ تست روی lab (فاز ۵)

قوانین کاربری را می‌شود روی lab با کاربران واقعی تست کرد. قوانین گروهی تا فاز ۱۰ به cache نیاز دارند.

<div dir="ltr">

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"
./install.sh --actor soroush        # ارتقا؛ policy و state حفظ می‌شوند

# p5.yaml را با یک کاربر و پروژه‌ی تستی واقعی بسازید:
#   apiVersion: git-policy/v1
#   kind: GitPolicy
#   metadata: {name: lab-phase5, revision: 2}
#   users:
#     USERNAME_TEST:
#       projects:
#         GROUP/PROJECT_TEST: {push: deny}
docker cp p5.yaml gitlab:/tmp/p5.yaml
$GP admin apply  --actor soroush --reason "phase5 lab" /tmp/p5.yaml
$GP admin enable --actor soroush --reason "phase5 lab"
$GP explain --user USERNAME_TEST --project GROUP/PROJECT_TEST     # انتظار: Verdict: REJECT
```

</div>

سپس:
- push با همان کاربر به همان پروژه → رد با `USER_PUSH_DENIED`
- push با همان کاربر به پروژه‌ی دیگر → قبول
- ویرایش یک فایل از Web UI گیت‌لب با همان کاربر → همان پیام در UI

<div dir="ltr">

```bash
docker exec gitlab sh -c 'grep REJECT /var/opt/gitlab/git-policy/logs/audit-*.jsonl | tail -n 2'
```

</div>

---

## ۱۴. فاز ۶: پیمایش ضد-دور‌زدن

### ۱۴.۱ مسئله

اگر فقط **آخرین commit** یا **وضعیت نهایی فایل‌ها** بررسی شود، این ترفندها از سیاست عبور می‌کنند:

| ترفند | چرا خطرناک است |
|---|---|
| commit A فایل DLL را اضافه، commit B آن را حذف می‌کند؛ هر دو با هم push می‌شوند | DLL برای همیشه در تاریخچه‌ی Git می‌ماند |
| یک فایل موجود (`readme.txt`) به `evil.dll` تغییر نام می‌دهد | blob جدیدی ساخته نمی‌شود |
| DLL در fork اضافه می‌شود و با Merge Request وارد پروژه‌ی اصلی می‌شود | GitLab `refs/merge-requests/*` را **بدون اجرای hook** می‌نویسد |
| تاریخچه‌ی `develop` (با قوانین سبک‌تر) به یک شاخه‌ی `release/*` (با قوانین سخت‌تر) push می‌شود | هیچ commit «جدیدی» برای repository وجود ندارد |
| یک tag مستقیماً به tree یا blob اشاره می‌کند | commit ندارد |

### ۱۴.۲ راه‌حل

<div dir="ltr">

```
برای هر ref در push:
  ۱. نوع شیء جدید:  commit | tag → commit | tag → tree | tag → blob       (git cat-file --batch-check)
  ۲. «قبلاً بررسی‌شده» = نوک شاخه‌ها و تگ‌های موجود (refs/heads و refs/tags)
       فقط آن‌هایی که «کلاس سیاست» برابر یا سخت‌گیرانه‌تر دارند
       (هرگز refs/merge-requests، keep-around و refهای داخلی GitLab)
  ۳. commitهای جدید = قابل رسیدن از نوک جدید، و نه از «قبلاً بررسی‌شده»   (git rev-list --stdin)
  ۴. برای **هر** commit جدید: مسیرهای اضافه/تغییر یافته                     (git diff-tree --stdin -c -z)
       - merge: فقط چیزی که خود merge اضافه کرده (بقیه در commitهای والد دیده می‌شوند)
       - rename = حذف + اضافه → نام جدید دیده می‌شود
       - submodule (gitlink) محتوا نیست و نادیده گرفته می‌شود
  ۵. tag → tree: همه‌ی مسیرهای tree؛  tag → blob: خود blob
```

</div>

- **حذف شاخه یا تگ** چیزی اضافه نمی‌کند. فقط قوانین هویتی (فاز ۵) روی آن اعمال می‌شوند.
- **force push**: فقط commitهای بازنویسی‌شده‌ی جدید بررسی می‌شوند.
- **شاخه‌ی جدید روی commitهای موجود**: هیچ پیمایشی لازم نیست (۳ میلی‌ثانیه).
- اولین `release/*` روی یک پروژه با قوانین سخت‌تر، تاریخچه‌ای را که هرگز با قوانین release بررسی نشده **دوباره بررسی می‌کند**. این عمدی است (تصمیم Q4).

### ۱۴.۳ Quarantine گیت

در pre-receive، شیءهای push‌شده هنوز در یک پوشه‌ی موقت (quarantine) هستند. git-policy متغیرهای `GIT_OBJECT_DIRECTORY`، `GIT_ALTERNATE_OBJECT_DIRECTORIES` و `GIT_QUARANTINE_PATH` را **دست نمی‌زند**؛ همه‌ی دستورات git آن‌ها را به ارث می‌برند. این موضوع در تست با push واقعی بررسی شد.

### ۱۴.۴ محدودیت‌ها و timeout

| تنظیم (`settings.limits`) | پیش‌فرض | اگر رد شود | قابل استثنا |
|---|---|---|---|
| `max_ref_updates` | 1000 | `LIMIT_REF_UPDATES` | ✅ |
| `max_new_commits` | 50000 | `LIMIT_COMMITS` | ✅ (مثلاً کاربر مهاجرت تاریخچه) |
| `max_new_blobs` | 500000 | `LIMIT_OBJECTS` | ✅ |
| `evaluation_timeout` | 45s | `EVAL_TIMEOUT` | ❌ |

همه‌ی این موارد **fail-closed** هستند: pushی که بررسی نشده، دور زدن سیاست است.

<div dir="ltr">

```yaml
exceptions:
  - id: EXC-IMPORT
    rules: [LIMIT_COMMITS]
    subjects: {users: [svc-import]}
    reason: import legacy history
    expires: 2026-10-31
```

</div>

### ۱۴.۵ کارایی

| سناریو | زمان اندازه‌گیری‌شده |
|---|---|
| repository با ۲۰٬۰۰۰ commit، push یک commit | ۵ میلی‌ثانیه |
| شاخه‌ی جدید روی همان repository | ۳ میلی‌ثانیه |

- تعداد پروسه‌های git در هر push **ثابت** است (cat-file، for-each-ref، یک rev-list برای هر ref، یک diff-tree) و به تعداد commitها بستگی ندارد.
- هزینه با تعداد **commitهای جدید** رشد می‌کند، نه با اندازه‌ی کل تاریخچه.
- اگر هیچ قانون محتوایی روی refهای push‌شده اعمال نشود، پیمایش **کلاً انجام نمی‌شود**.

### ۱۴.۶ دستور scan (عیب‌یابی)

نشان می‌دهد یک push دقیقاً چه چیزهایی را وارد repository می‌کند:

<div dir="ltr">

```bash
cd /path/to/repo.git
echo "<old-sha> <new-sha> refs/heads/main" | git-policy scan --policy policy.yaml --project finance/app
```

</div>

<div dir="ltr">

```
commits=2 blobs=1 entries=1 exclusion-tips=1 duration=4ms
  refs/heads/main                55e85808ff46 100644 "Lib/Mic.Caching.dll"
```

</div>

> در فاز ۶ این مسیرها **فقط پیدا می‌شوند**. فاز ۷ آن‌ها را با `blocked_extensions` و `blocked_paths` مقایسه می‌کند، و فاز ۸ حجم و امضای PE را بررسی می‌کند.

---

## ۱۵. فاز ۷: پسوند و مسیر فایل

### ۱۵.۱ چه چیزی رد می‌شود؟

هر مسیری که پیمایش فاز ۶ پیدا می‌کند (در **هر** commit جدید) با قوانین مؤثر همان پروژه و شاخه مقایسه می‌شود:

| قانون | مثال | کد |
|---|---|---|
| `blocked_extensions` | `dll`، `exe`، `pdb`، `zip`، `tar.gz` | `BLOCKED_EXTENSION` |
| `blocked_paths` | `**/obj/**`، `**/bin/Debug/**`، `packages/**` | `BLOCKED_PATH` |

**ترفندهای نام فایل** هم شناسایی می‌شوند. هم شکل خام و هم شکل «ویندوزی» نام بررسی می‌شود:

| نام فایل در Git | چرا خطرناک است | نتیجه |
|---|---|---|
| `TEST.DLL`، `Test.Dll` | حروف بزرگ | ❌ رد |
| `evil.dll.`، `evil.dll ` | ویندوز نقطه/فاصله‌ی انتهایی را حذف می‌کند | ❌ رد |
| `evil.dll::$DATA` | stream پیش‌فرض NTFS | ❌ رد |
| `README.md` → تغییر نام به `README.dll` | blob جدیدی ساخته نمی‌شود | ❌ رد |
| فایل `.dll` که با LFS ذخیره شده | فایل اشاره‌گر LFS هم همان نام را دارد | ❌ رد (تصمیم Q6) |

### ۱۵.۲ فایل‌های قدیمی (legacy) در repository

| کار | نتیجه | چرا |
|---|---|---|
| **حذف** یک DLL قدیمی | ✅ قبول | هدف همین است |
| **تغییر** یک DLL قدیمی | ❌ رد | نسخه‌ی جدید یک blob جدید با نام ممنوع است |
| push بدون دست زدن به DLL قدیمی | ✅ قبول | فقط محتوای **جدید** بررسی می‌شود |

### ۱۵.۳ پیام توسعه‌دهنده

<div dir="ltr">

```
remote: GL-HOOK-ERR: Push rejected by organizational Git policy.
remote: GL-HOOK-ERR: User: dev
remote: GL-HOOK-ERR: Project: finance/app
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Rule: BLOCKED_EXTENSION
remote: GL-HOOK-ERR: Ref: refs/heads/main
remote: GL-HOOK-ERR: Commit: 5c1f0e2a...
remote: GL-HOOK-ERR: File: Lib/Mic.Caching.dll
remote: GL-HOOK-ERR: Blocked extension: .dll
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Required action (BLOCKED_EXTENSION): Publish binary dependencies as NuGet packages to Nexus instead of committing them.
remote: GL-HOOK-ERR: Help: #devops-help
```

</div>

- متن «Required action» از `settings.messages.remediation` در policy می‌آید. آدرس Nexus و روش کار با NuGet را همان‌جا بنویسید.
- Nexus فقط در متن پیام است. در دسترس نبودن Nexus **هیچ اثری** روی بررسی push ندارد.
- حداکثر ۲۰ مورد در پیام نشان داده می‌شود و حداکثر ۱۰۰۰ مورد در هر push ثبت می‌شود. بقیه با یک یادداشت «more findings» خلاصه می‌شوند.

### ۱۵.۴ رفع مشکل توسط توسعه‌دهنده

اگر DLL در یک commit قدیمی‌تر همین push اضافه شده باشد، حذف آن در commit بعدی **کافی نیست**، چون در تاریخچه می‌ماند. باید تاریخچه‌ی محلی بازنویسی شود:

<div dir="ltr">

```bash
git rebase -i origin/main          # commit مربوطه را edit کنید و فایل را حذف کنید
# یا:
git reset --soft origin/main && git rm --cached Lib/*.dll && git commit -m "..."
```

</div>

### ۱۵.۵ برنامه‌ی جایگزینی PoC روی lab (پیشنهادی)

<div dir="ltr">

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"
./install.sh --actor soroush                       # ارتقا به نسخه‌ی فاز ۷
```

</div>

**گام ۱: حالت audit.** git-policy فقط ثبت می‌کند و PoC همچنان رد می‌کند.

policy با `mandatory: {mode: audit, blocked_extensions: [dll, exe]}` و `settings: {mode: audit}` و یک revision جدید:

<div dir="ltr">

```bash
$GP admin apply  --actor soroush --reason "phase7 shadow" /tmp/p7.yaml
# چند push معمولی و یک push با DLL؛ سپس:
docker exec gitlab sh -c 'grep WOULD_REJECT /var/opt/gitlab/git-policy/logs/audit-*.jsonl | tail'
```

</div>

**گام ۲: حالت enforce.** اکنون هم PoC و هم git-policy رد می‌کنند.

همان policy با `mode: enforce` و revision بالاتر. push با DLL باید پیام `Rule: BLOCKED_EXTENSION` را نشان دهد. این را از CLI و از Web UI امتحان کنید.

**گام ۳: بازنشسته کردن PoC** (غیرمخرب؛ یک نسخه در `backup/` می‌ماند و در audit log ثبت می‌شود):

<div dir="ltr">

```bash
$GP admin retire-hook --actor soroush --name 01-block-dll
docker exec gitlab ls -l /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/    # فقط 50-git-policy
```

</div>

بعد از آن push با DLL باید **فقط** با پیام git-policy رد شود.

برای برگرداندن PoC (در صورت نیاز):

<div dir="ltr">

```bash
docker exec -u root gitlab sh -c 'cp /var/opt/gitlab/git-policy/backup/01-block-dll.retired.* /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll && chmod 0755 /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll'
```

</div>

### ۱۵.۶ رویدادهای جدید audit

| action | معنی |
|---|---|
| `REJECT` (با `file` و `commit`) | فایل ممنوع؛ push رد شد |
| `WOULD_REJECT` | در حالت audit: رد **می‌شد**، ولی push قبول شد |
| `EXCEPTION_APPLIED` (با `file`) | یک استثنا این فایل را مجاز کرد |
| `FINDINGS_TRUNCATED` | بیش از ۱۰۰۰ مورد در یک push |
| `HOOK_RETIRED` | یک hook دیگر (مثلاً PoC) به backup منتقل شد |

---

## ۱۶. فاز ۸: حجم فایل و تشخیص فایل اجرایی از روی محتوا

### ۱۶.۱ حجم فایل (`FILE_TOO_LARGE`)

- حجم هر blob جدید از **header شیء Git** خوانده می‌شود (`git cat-file --batch-check`). **هیچ checkoutی انجام نمی‌شود و محتوای فایل خوانده نمی‌شود.** یک فایل ۱۹ مگابایتی فقط از روی اندازه‌اش رد شد.
- حد مؤثر همان قاعده‌ی فاز ۲ است: دقیق‌ترین سطح (ref > project > namespace > defaults)، ولی **هرگز بالاتر از `mandatory.max_file_size`**.
- دقیقاً برابر حد مجاز است؛ یک بایت بیشتر رد می‌شود.

| حجم فایل | نتیجه |
|---|---|
| ≤ حد مؤثر | ✅ قبول |
| بیشتر از حد مؤثر، ولی ≤ سقف mandatory | ❌ رد؛ یک استثنای **معمولی** با `max_file_size` کافی است |
| بیشتر از سقف mandatory | ❌ رد؛ فقط استثنای **`mandatory: true`** با `max_file_size` کافی |

<div dir="ltr">

```yaml
mandatory:
  max_file_size: 50MiB             # سقف سازمانی
defaults:
  max_file_size: 20MiB
projects:
  media/site: {max_file_size: 40MiB}
exceptions:
  - id: EXC-MODEL
    rules: [FILE_TOO_LARGE]
    mandatory: true                  # چون از سقف 50MiB بالاتر است
    subjects: {users: [ds1]}
    scope: {projects: [analytics/forecast]}
    max_file_size: 200MiB            # فقط تا این حد، نه نامحدود
    reason: model snapshots until the artifact store exists
    expires: 2026-12-15
```

</div>

پیام توسعه‌دهنده:

<div dir="ltr">

```
remote: GL-HOOK-ERR: Rule: FILE_TOO_LARGE
remote: GL-HOOK-ERR: File: data/large.bin
remote: GL-HOOK-ERR: Size: 3.0 MiB (limit 2.0 MiB)
remote: GL-HOOK-ERR: Required action (FILE_TOO_LARGE): Store large artifacts in Nexus ...
```

</div>

**دور زدن‌ها هم پوشش داده شده‌اند:**
- فایل بزرگی که در یک commit اضافه و در commit بعدی همان push حذف شود رد می‌شود.
- tagی که مستقیم به یک blob بزرگ اشاره کند رد می‌شود.

> **Git LFS:** فایل‌هایی که با LFS ذخیره می‌شوند در Git فقط یک اشاره‌گر ~۱۳۰ بایتی هستند و این قانون حجم **واقعی** آن‌ها را نمی‌بیند. برای LFS از تنظیمات خود GitLab (محدودیت حجم LFS و repository) استفاده کنید.

### ۱۶.۲ تشخیص فایل اجرایی ویندوز (`BLOCKED_SIGNATURE: pe`)

نام فایل را می‌شود عوض کرد (`Mic.Caching.dll` → `readme.txt`)، ولی **محتوا** را نه. با `blocked_signatures: [pe]`، هر فایلی که header آن یک فایل اجرایی یا کتابخانه‌ی ویندوز باشد (exe، dll، sys، …) رد می‌شود، **هر نامی که داشته باشد**.

<div dir="ltr">

```yaml
namespaces:
  finance:
    refs:
      "refs/heads/release/**":
        blocked_signatures: [pe]
```

</div>

- تشخیص: `MZ` در ابتدای فایل **و** `PE\0\0` در آدرسی که در offset `0x3C` نوشته شده. فقط `MZ` کافی نیست، پس یک فایل متنی که با «MZ» شروع شود رد نمی‌شود (تست شده).
- فقط **ابتدای** فایل (حداکثر ۶۴ KiB) استفاده می‌شود. ولی Git برای خواندن، هر شیء را کامل از حالت فشرده باز می‌کند، پس هزینه با حجم فایل‌های بررسی‌شده رشد می‌کند. به همین دلیل این قانون **فقط** برای کلاس‌هایی اجرا می‌شود که آن را فعال کرده‌اند (مثلاً شاخه‌های release) و در حالت پیش‌فرض خاموش است.

### ۱۶.۳ اولین شاخه‌ی «سخت‌گیرتر» (تصمیم Q4)

وقتی قوانین یک شاخه سخت‌گیرانه‌تر است (مثلاً `release/**` با `pe`)، **اولین** push به آن کلاس، تاریخچه‌ای را که هرگز با این قوانین بررسی نشده دوباره بررسی می‌کند. این عمدی است، چون جلوی ترفند «develop → release» را می‌گیرد.

نتیجه‌ی عملی: اگر در تاریخچه‌ی `main`
- فایل‌های قدیمی (legacy) هست که با قوانین release ممنوع‌اند، یا
- فایلی هست که با استثنایی **فقط برای `main`** مجاز شده،

آن‌وقت ساختن اولین شاخه‌ی release رد می‌شود و پیام دقیقاً فایل و commit را نشان می‌دهد. راه‌حل: پاک‌سازی تاریخچه، یا یک استثنای صریح برای `refs/heads/release/**`. از آن به بعد شاخه‌های release بعدی فقط محتوای جدید را بررسی می‌کنند.

### ۱۶.۴ کارایی

| سناریو | زمان کل push (محلی) |
|---|---|
| ۳۰۰ فایل جدید، هر کدام ۲۰ KiB | ۲۵۰ میلی‌ثانیه |
| یک فایل ۱۹ MiB (رد از روی header) | ۱٫۰۷ ثانیه (عمدتاً انتقال داده) |

تعداد پروسه‌های git ثابت است: یک `cat-file --batch-check` برای همه‌ی اندازه‌ها، و در صورت نیاز یک `cat-file --batch` برای امضاها.

---

## ۱۷. فاز ۹: audit log کامل

### ۱۷.۱ تنظیمات

<div dir="ltr">

```yaml
settings:
  audit:
    log_accepted: true      # برای هر push قبول‌شده هم یک رویداد ACCEPT ثبت شود (پیش‌فرض: false)
    required: false         # true: اگر ثبت در audit log ممکن نباشد، push رد شود (AUDIT_UNAVAILABLE)
    retention_days: 180     # فایل‌های قدیمی‌تر با admin prune-logs حذف می‌شوند
```

</div>

| تنظیم | پیش‌فرض | اثر |
|---|---|---|
| `log_accepted` | `false` | رویداد `ACCEPT` شامل کاربر، پروژه، refها (old/new)، تعداد commitها و مدت بررسی |
| `required` | `false` | `true` یعنی «pushی که ثبت نشود، قبول نشود». برای محیط‌هایی با الزام ممیزی سخت‌گیرانه |
| `retention_days` | `180` | `admin prune-logs` (یا فعل `prune-logs` در Jenkins) فایل‌های قدیمی‌تر را حذف می‌کند؛ فایل امروز هرگز حذف نمی‌شود |

### ۱۷.۲ ساختار هر رویداد

هر خط یک JSON کامل است (JSON Lines) و این فیلدهای ثابت را دارد:

| فیلد | توضیح |
|---|---|
| `timestamp` | زمان با منطقه‌ی زمانی سرور |
| `action` | `REJECT`، `ACCEPT`، `WOULD_REJECT`، `EXCEPTION_APPLIED`، `POLICY_UPDATED`، `POLICY_ENABLED`، … |
| `schema` | `git-policy/audit/v1` |
| `event_id` | شناسه‌ی یکتا؛ برای حذف تکراری‌ها هنگام ارسال به SIEM |
| `host` | نام host (برای چند سرور) |
| `engine_version` | نسخه‌ی git-policy |
| `user`، `gl_id`، `project`، `repository`، `protocol` | زمینه‌ی push از Gitaly |
| `rule`، `source`، `ref`، `commit`، `file`، `size` | برای تخلف‌ها |
| `actor`، `reason` | برای عملیات مدیریتی |

### ۱۷.۳ ارسال به SIEM

فایل‌ها در مسیر volume داده‌ی GitLab روی host هستند (`…/git-policy/logs/audit-*.jsonl`). هر ابزار جمع‌آوری لاگ (Filebeat، Vector، Fluent Bit) می‌تواند آن‌ها را به‌صورت JSON بخواند. نمونه برای Vector:

<div dir="ltr">

```toml
[sources.git_policy]
type    = "file"
include = ["/srv/gitlab/data/git-policy/logs/audit-*.jsonl"]   # host path of /var/opt/gitlab
[transforms.parse]
type   = "remap"
inputs = ["git_policy"]
source = ". = parse_json!(.message)"
```

</div>

---

## ۱۸. lab محلی و تست روی GitLab واقعی

### ۱۸.۱ بالا آوردن

<div dir="ltr">

```bash
cd ~/infra/git-policy
tools/build.sh all
lab/up.sh                           # GitLab CE 17.10.5 + Jenkins + gp-ctl
tests/gitlab/verify-gitlab.sh       # all phases on the real GitLab -> RESULT: 85 passed, 0 failed
tests/gitlab/verify-jenkins.sh      # Phase 11: Jenkins + GitOps + approvals -> RESULT: 46 passed, 0 failed
lab/down.sh                         # stop (data kept);  lab/down.sh --purge  deletes everything
```

</div>

| سرویس | آدرس | ورود |
|---|---|---|
| GitLab | http://localhost:8080 (ssh 2222) | `root` / رمز در `lab/.env` |
| Jenkins | http://localhost:8081 | `admin` / رمز در `lab/.env` — job آزمایشی `git-policy-status` |

### ۱۸.۲ چه چیزهایی روی GitLab واقعی تأیید شد

| موضوع | نتیجه |
|---|---|
| نصب با `install.sh` (غیرمخرب، شروع خاموش)، ارتقا، `deploy` از Jenkins | ✅ |
| متغیرهای واقعی Gitaly: `GL_USERNAME`، `GL_PROJECT_PATH`، `GL_REPOSITORY`، `GL_PROTOCOL` | ✅ |
| قوانین کاربر روی **HTTP**، **SSH** و **Web UI/API** | ✅ پیام ما در Web UI هم نمایش داده می‌شود |
| قوانین گروه با cache عضویت | ✅ |
| DLL اضافه و حذف در یک push، ترفند NTFS، مسیر ممنوع، استثنای محدود | ✅ |
| **Merge Request از fork** با فایل ممنوع | ✅ GitLab: «Branch cannot be merged»؛ جزئیات در audit log |
| حجم فایل، امضای PE روی release | ✅ |
| wiki (فقط قوانین mandatory) | ✅ `GL_PROJECT_PATH=…/app.wiki`، `GL_REPOSITORY=wiki-N` |
| fail-closed (state خراب)، break-glass، rollback، disable با TTL | ✅ |
| `ACCEPT`، `audit.required`، `prune-logs`، `logs`، `backup` | ✅ |
| بازنشسته کردن PoC | ✅ |
| کانال Jenkins → gp-ctl → git-policy-ctl و رد دستورهای خطرناک | ✅ |
| همگام‌سازی گروه‌ها با job جنکینز، محافظ کاهش ۳۰٪، `FORCE`، W010 | ✅ |
| **TEST 17**: Jenkins خاموش است → enforcement ادامه دارد | ✅ |
| **TEST 18**: GitLab API در دسترس نیست → cache قبلی دست‌نخورده می‌ماند و قوانین گروه کار می‌کنند | ✅ |

### ۱۸.۳ یافته‌های مهم از GitLab واقعی

1. **deploy key:** `GL_USERNAME` نام کاربری **سازنده‌ی کلید** است (در تست `root`) و `GL_ID=key-N`. یعنی قوانین کاربری **و گروهی** آن شخص روی deploy keyهای او هم اعمال می‌شود. در تست، GitLab سازنده‌ی گروه (`root`) را خودکار عضو گروه `contractors` کرده بود، و به همین دلیل push با deploy key او رد شد. deploy keyها را با یک حساب سرویس مخصوص بسازید.
2. **merge کردن MR:** GitLab پیام hook را در صفحه‌ی MR نشان نمی‌دهد و فقط می‌گوید «Branch cannot be merged». دلیل دقیق در audit log هست و با `explain` هم قابل بررسی است.
3. **Web UI:** خطوط پیام با `<br>` نمایش داده می‌شوند و کاربر کل پیام (قانون، فایل، اقدام لازم) را می‌بیند.
4. **دو باگ در `git-policy-ctl`** فقط در lab پیدا و رفع شدند:
   - sudo به‌طور پیش‌فرض `SSH_ORIGINAL_COMMAND` را پاک می‌کند؛ `env_keep` مخصوص همین دستور لازم است.
   - regex در bash تکرار بیشتر از ۲۵۵ را قبول نمی‌کند.
5. **دو باگ در pipeline و ابزار sync** هم فقط با Jenkins واقعی پیدا شدند:
   - در اولین اجرای یک job جدید، پارامترهای pipeline هنوز تعریف نشده‌اند (`FORCE` خالی بود).
   - credential فایل‌محور Jenkins یک newline به انتهای token اضافه می‌کرد.

   هر دو رفع شدند: موتور حالا فاصله‌های اضافه‌ی token را حذف می‌کند و token دارای کاراکتر کنترلی را با پیام واضح رد می‌کند.
6. **تأخیر:** یک push کامل HTTP روی lab با git-policy فعال حدود ۴۰۰ میلی‌ثانیه طول کشید، که بیشترش خود GitLab است.

---

## ۱۹. فاز ۱۰: همگام‌سازی عضویت گروه‌ها از GitLab

### ۱۹.۱ جریان کار

<div dir="ltr">

```
Jenkins job: git-policy-sync-membership  (هر ۱۵ دقیقه + اجرای دستی)
  │ ۱. ctl groups              ← گروه‌هایی که policy فعال سرور استفاده می‌کند
  │ ۲. git-policy sync-membership --gitlab-url … --groups …
  │      ← فقط همان گروه‌ها از GitLab API؛ token فقط در credentials جنکینز
  │ ۳. ctl apply-membership < membership.json
  ▼                            ← سرور: بررسی، محافظ ایمنی، نوشتن اتمیک (+ previous.json)
membership/current.json        ← hook فقط این فایل محلی را می‌خواند
```

</div>

- **token هرگز روی سرور GitLab ذخیره نمی‌شود** و در console جنکینز هم ماسک می‌شود (در تست بررسی شد).
- **عضویت طبق منطق خود GitLab است:** کاربر عضو گروه G است اگر مستقیم یا از طریق گروه‌های **والد** عضو باشد. عضویت در **زیرگروه** شخص را عضو گروه والد نمی‌کند.

### ۱۹.۲ محافظ‌های ایمنی (سمت سرور)

`admin apply-membership` در این حالت‌ها **رد می‌کند**، مگر با `FORCE=true` در Jenkins (یا `--force`):

| حالت | چرا |
|---|---|
| تعداد عضویت‌ها بیش از **۳۰٪** کم شده (وقتی قبلاً حداقل ۱۰ عضویت بوده) | معمولاً نشانه‌ی خرابی API یا تغییر مجوز token است، نه تغییر واقعی |
| گروهی که policy فعال استفاده می‌کند در فایل نیست | فایل ناقص است |
| داده قدیمی‌تر از `hard_max_age` است | فایل کهنه نباید جایگزین فایل تازه شود |
| `generated_at` در آینده است | مشکل ساعت |

اگر یک گروه policy در GitLab **وجود نداشته باشد**، sync کلاً شکست می‌خورد و **هیچ فایلی** نوشته نمی‌شود.

### ۱۹.۳ هشدار W010: نام‌هایی که در GitLab نیستند

sync یک **inventory** (همه‌ی کاربران، گروه‌ها و پروژه‌ها) هم می‌سازد. validator با آن غلط‌های املایی را پیدا می‌کند:

<div dir="ltr">

```bash
git-policy sync-membership --gitlab-url https://gitlab.example.com --policy policy.yaml \
    -o membership.json --inventory-out inventory.json          # token: $GITLAB_TOKEN
git-policy validate --inventory inventory.json policy.yaml
#   WARNING W010  users.alexx: user "alexx" does not exist in GitLab (inventory)
```

</div>

### ۱۹.۴ عملیات

| کار | چطور |
|---|---|
| اجرای فوری sync | Jenkins ← `git-policy-sync-membership` ← Build |
| عبور از محافظ بعد از بررسی GitLab | Build with Parameters ← `FORCE=true` |
| دیدن وضعیت cache | `ctl status` یا job `git-policy-status` ← خط `Membership cache: fresh (…)` |
| اگر GitLab API در دسترس نیست | کاری لازم نیست: sync شکست می‌خورد و cache قبلی می‌ماند؛ بعد از ۲۴ ساعت allowهای گروهی نادیده گرفته می‌شوند (فقط محدود می‌کند) |

---

## ۲۰. فاز ۱۱: Jenkins (رابط کاربری) با GitOps

### ۲۰.۱ مدل کار

<div dir="ltr">

```
توسعه‌دهنده‌ی policy ── MR ──> repo: platform/git-policy-config (main محافظت‌شده)
                                   ├── test/policy.yaml
                                   └── production/policy.yaml
                                            │ push به main
                                            ▼
        Jenkins: git-policy-gitops (هر ۲ دقیقه poll یا webhook)
           ├── validate هر دو فایل
           ├── TEST: اعمال خودکار + بررسی sha256
           └── PRODUCTION: فقط گزارش «drift» (UNSTABLE)
                                            │ انسان
                                            ▼
        Jenkins: git-policy  ACTION=UPDATE_POLICY  ENVIRONMENT=PRODUCTION
           validate → diff با نسخه‌ی فعال → تأیید نفر دوم → apply → بررسی sha256
```

</div>

- **همه‌ی تغییرات policy در git است:** چه کسی، چه چیزی، کی، چرا (پیام commit و MR). rollback هم یعنی revert در git یا `ROLLBACK_POLICY`.
- **`metadata.revision` باید در هر تغییر بالا برود.** سرور revision برابر یا کمتر را رد می‌کند (`V040`)، پس تاریخچه تصادفی بازنویسی نمی‌شود.
- **Jenkins فقط مدیریت می‌کند.** اگر Jenkins خاموش باشد، enforcement ادامه دارد (TEST 17، تست‌شده).

### ۲۰.۲ job اصلی: `git-policy`

| پارامتر | توضیح |
|---|---|
| `ACTION` | `STATUS`، `VALIDATE_POLICY`، `UPDATE_POLICY`، `ENABLE`، `DISABLE`، `ROLLBACK_POLICY`، `BACKUP_POLICY`، `SYNC_GROUP_MEMBERSHIP`، `EXPLAIN`، `DEPLOY`، `RETIRE_HOOK`، `PRUNE_LOGS`، `EXPORT_LOGS` |
| `ENVIRONMENT` | `TEST` یا `PRODUCTION` |
| `REASON` | **الزامی برای هر تغییر** (۱۰ تا ۵۰۰ کاراکتر، یک خط)؛ در audit log ثبت می‌شود |
| `POLICY_REF` | شاخه، تگ یا commit در repo policy (پیش‌فرض `main`) |
| `DISABLE_TTL` | `30m` … `24h` |
| `ROLLBACK_TO` | شماره‌ی نسخه (خالی = نسخه‌ی قبلی) |
| `FORCE` | برای sync: عبور از محافظ ۳۰٪ |
| `EXPLAIN_USER`، `EXPLAIN_PROJECT`، `EXPLAIN_REF`، `EXPLAIN_GROUPS` | برای `EXPLAIN` |
| `HOOK_NAME` | برای `RETIRE_HOOK` |
| `LOG_DATE` | برای `EXPORT_LOGS` |

**چیزی که هر اکشن انجام می‌دهد و artifactهایی که در Jenkins ذخیره می‌شوند:**

| اکشن | مراحل | artifact |
|---|---|---|
| `VALIDATE_POLICY` | checkout، validate با inventory گیت‌لب (W010)، validate دوم روی سرور با باینری مستقر‌شده | `validate.txt`، `validate.json`، `validate-server.json` |
| `UPDATE_POLICY` | همان + **diff** با policy فعال + (PRODUCTION: تأیید) + apply + بررسی sha256 | + `policy.diff`، `policy.sha256` |
| `BACKUP_POLICY` | tar.gz از policyها، state و membership | `git-policy-backup-<ENV>-<زمان>.tgz` + sha256 |
| `EXPORT_LOGS` | audit log یک روز | `audit-<ENV>-<تاریخ>.jsonl` |
| `EXPLAIN` | «چرا این push رد/قبول می‌شود؟» | `explain.json` |
| `STATUS` | وضعیت کامل؛ CRITICAL → build ناپایدار (UNSTABLE) | `status.json` |

### ۲۰.۳ تأیید در PRODUCTION (چهار چشم)

- این اکشن‌ها در PRODUCTION قبل از اجرا **منتظر تأیید** می‌مانند: `UPDATE_POLICY`، `DISABLE`، `ROLLBACK_POLICY`، `DEPLOY`، `RETIRE_HOOK`، و `SYNC_GROUP_MEMBERSHIP` با `FORCE`.
- فقط کاربران `GP_APPROVERS` می‌توانند تأیید کنند، حداکثر تا ۳۰ دقیقه.
- **کسی که درخواست داده نمی‌تواند خودش تأیید کند.** اگر admin درخواست خودش را تأیید کند، build با پیام `four-eyes rule` شکست می‌خورد و هیچ چیزی اعمال نمی‌شود (تست شد).
- نام درخواست‌کننده، commit و تأییدکننده در audit log سرور ثبت می‌شود، مثلاً:
  `jenkins:git-policy#27 by:admin commit:9ea7e78c2b3d approved-by:approver`
- `ENABLE` تأیید لازم ندارد، چون حفاظت را **برمی‌گرداند**.

### ۲۰.۴-الف. نتیجه‌ی تست روی lab (۴۶ از ۴۶)

- همه‌ی ۱۳ اکشن از طریق Jenkins اجرا شدند.
- **GitOps:**
  - push به repo منجر به اعمال خودکار روی TEST شد.
  - policy نامعتبر (`20MB`) به سرور نرسید.
  - تغییر بدون بالا بردن revision رد شد (`V040`).
- **PRODUCTION:**
  - تأیید توسط خود درخواست‌کننده رد شد (چهار چشم).
  - تأیید توسط approver اعمال شد.
  - رد کردن DISABLE توسط approver باعث ABORTED شد و موتور روشن ماند.
- در لاگ هیچ build، هیچ token یا کلید خصوصی دیده نشد.

**باگ‌هایی که فقط Jenkins واقعی نشان داد و رفع شدند:**
- sandbox گرووی دسترسی پویا مثل `env[...]` و `.take()` را اجازه نمی‌دهد.
- Jenkins پارامتر رشته‌ای **خالی** را به‌صورت متغیر محیطی تعریف نمی‌کند.
- job ‌هایی که JCasC در لحظه‌ی بالا آمدن می‌سازد تا restart بعدی ثبت نمی‌شوند (`up.sh` خودکار حلش می‌کند).
- `git-policy-ctl` بدون `SSH_CLIENT` با خطا متوقف می‌شد.

> **نکته‌ی lab:** در lab فقط یک GitLab هست و TEST و PRODUCTION به همان اشاره می‌کنند. به همین دلیل job gitops همیشه «drift» گزارش می‌دهد، و revision فایل production باید از revision اعمال‌شده روی TEST بالاتر باشد. روی سرورهای واقعی جدا، این محدودیت وجود ندارد.

### ۲۰.۴ job ‌های دیگر

| job | کار |
|---|---|
| `git-policy-gitops` | GitOps: اعمال خودکار روی TEST و گزارش drift برای PRODUCTION |
| `git-policy-sync-membership` | همگام‌سازی گروه‌ها هر ۱۵ دقیقه (فاز ۱۰) |
| `git-policy-status` | وضعیت سریع (lab) |

### ۲۰.۵ راه‌اندازی روی سرورهای واقعی

**۱. روی Docker host گیت‌لب** (یک بار، با root):

<div dir="ltr">

```bash
ssh-keygen -t ed25519 -N '' -f jenkins_ctl_key          # on a secure machine; private key -> Jenkins credential
sudo deploy/install-ctl.sh --pubkey jenkins_ctl_key.pub --dry-run
sudo deploy/install-ctl.sh --pubkey jenkins_ctl_key.pub
ssh-keyscan -t ed25519 <gitlab-docker-host>            # -> known_hosts file for GP_SSH_KNOWN_HOSTS
```

</div>

این اسکریپت کاربر `gitpolicy-deploy` را **بدون** عضویت در گروه docker می‌سازد و `git-policy-ctl` را نصب می‌کند. فایل sudoers را با `visudo -c` بررسی می‌کند و کلید را با forced command ثبت می‌کند. روی یک host شبیه‌سازی‌شده تست شده است.

**۲. در Jenkins:**
- **pluginها:** `workflow-aggregator`، `git`، `ssh-agent`، `credentials-binding`، `pipeline-utility-steps`، `timestamper`، `ws-cleanup`
- **credentials:**
  - `git-policy-ssh-test` و `git-policy-ssh-prod` (کلید SSH)
  - `gitlab-api-readonly` (token با `read_api`)
  - `git-policy-config-read` (deploy token گیت‌لب برای خواندن repo policy)
- **متغیرهای سراسری** (Manage Jenkins → System → Global properties): فهرست کامل در سرآیند `jenkins/Jenkinsfile` است (`GP_POLICY_REPO`، `GP_CTL_TEST`، `GP_CTL_PRODUCTION`، `GP_APPROVERS`، `GP_BIN`، `GP_SSH_KNOWN_HOSTS`، …).
- **دو job از نوع «Pipeline script from SCM»** روی repo همین پروژه: `jenkins/Jenkinsfile` با نام `git-policy`، و `jenkins/Jenkinsfile.gitops` با نام `git-policy-gitops`.
- **باینری `git-policy` روی agent** (مسیر `GP_BIN`): از `tools/build.sh build` یا از artifact یک job ساخت.

**۳. در GitLab:**
- repo `platform/git-policy-config` با شاخه‌ی `main` محافظت‌شده و MR approval.
- (اختیاری) webhook روی push به job `git-policy-gitops` به جای poll.

---

## ۲۱. واژه‌نامه

| واژه | معنی |
|---|---|
| **pre-receive hook** | اسکریپتی که Git قبل از پذیرش push اجرا می‌کند؛ کد خروج غیرصفر = رد push |
| **Gitaly** | سرویس ذخیره‌سازی Git در GitLab که hookها را اجرا می‌کند |
| **Fail-closed** | در صورت خطا، push رد شود (امن‌تر) |
| **Fail-open** | در صورت خطا، push پذیرفته شود (دسترس‌پذیرتر) |
| **Atomic rename** | جایگزینی فایل به‌گونه‌ای که خواننده یا نسخه‌ی کامل قدیم را ببیند یا کامل جدید |
| **Break-glass** | راه اضطراری دور زدن موتور، فقط با دسترسی root و همراه با ثبت |
| **ACTIVE / PREVIOUS** | اشاره‌گر به نسخه‌ی فعال و نسخه‌ی قبلی سیاست |
| **revision** | شماره‌ی بازنگری که نویسنده‌ی سیاست در فایل می‌گذارد |
| **version** | شماره‌ی نسخه‌ی ذخیره‌شده روی سرور (`000001`, `000002`, …) |
| **Audit log** | لاگ ساختاریافته (JSON Lines) از همه‌ی رویدادهای امنیتی و مدیریتی |
| **Membership cache** | فایل محلی عضویت کاربران در گروه‌های GitLab؛ push هرگز منتظر GitLab API نمی‌ماند |
| **explain** | شبیه‌سازی تصمیم policy برای یک push فرضی، بدون push واقعی |
| **Quarantine** | پوشه‌ی موقتی که Git شیءهای push‌شده را تا پایان pre-receive در آن نگه می‌دارد |
| **Audit mode (shadow)** | حالتی که تخلف فقط با `WOULD_REJECT` ثبت می‌شود و push رد نمی‌شود؛ برای rollout امن |
| **امضای PE** | ساختار header فایل‌های اجرایی ویندوز (MZ … PE\0\0)؛ مستقل از نام فایل |
| **کلاس سیاست** | مجموعه‌ی refهایی که قوانین محتوایی یکسان دارند؛ برای تعیین «قبلاً بررسی‌شده» استفاده می‌شود |

</div>
