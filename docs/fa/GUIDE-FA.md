# راهنمای فارسی git-policy

> نسخه نرم‌افزار: **v0.1.0** — نسخه schema سیاست: **`git-policy/v1`**
> وضعیت: فازهای ۱ تا ۷ انجام شده. قوانین **هویت** (کاربر، گروه، پروژه، ref، استثنا) و قوانین **پسوند و مسیر فایل** (DLL، EXE، …) روی **همه‌ی commitهای جدید** اجرا می‌شوند. حجم فایل و امضای PE در فاز ۸ اضافه می‌شوند.
>
> **رابط کاربری نهایی این سیستم Jenkins است.** دستورهای `docker exec ... admin` در این راهنما فقط برای تست lab در همین مراحل‌اند؛ در فاز ۱۱ همه‌ی ورودی‌ها و عملیات از طریق Jenkins انجام می‌شود.

---

## ۱. git-policy چیست؟

یک لایه‌ی **اجرای سیاست سازمانی روی سرور Git** برای GitLab Self-Managed است که از طریق **Global Pre-Receive Hook** در Gitaly اجرا می‌شود.
هر `git push` قبل از ثبت، توسط این موتور بررسی می‌شود و یا **پذیرفته** یا **رد** می‌شود.

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
| ۷ | پسوند و مسیر فایل (DLL, EXE, ...)، حالت audit، بازنشسته کردن PoC | ✅ **انجام شده (منتظر تأیید)** |
| ۸ | حجم blob و امضای PE | ⏳ |
| ۹ به بعد | Audit کامل، cache گروه‌ها، Jenkins، تست، امنیت، کارایی، مستندات | ⏳ |

> ✅ از فاز ۷ به بعد **git-policy خودش DLL/EXE و مسیرهای ممنوع را رد می‌کند.** PoC قدیمی (`01-block-dll`) تا زمانی که شما روی lab تأیید کنید دست‌نخورده می‌ماند و بعد با `admin retire-hook` بازنشسته می‌شود (بخش ۱۵.۵).

---

## ۳. ساختار پروژه

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

---

## ۴. ساخت (Build) و تست

نیازی به نصب Go ندارید؛ همه‌چیز داخل کانتینر (`golang:1.24-alpine` به‌علاوه‌ی git) اجرا می‌شود.

```bash
cd ~/infra/git-policy

tools/build.sh test      # gofmt + go vet + تست‌های واحد
tools/build.sh build     # ساخت dist/git-policy-0.1.0-linux-amd64 + فایل sha256
tools/build.sh all       # هر دو

tests/integration/phase4.sh   # ۳۷ تست: نصب، روشن/خاموش، apply/rollback، fail-closed
tests/integration/phase5.sh   # ۴۰ تست: قوانین کاربر/گروه/استثنا با git push واقعی
tests/integration/phase6.sh   # ۲۳ تست: پیمایش ضد-دور‌زدن داخل quarantine گیت، محدودیت‌ها، کارایی
tests/integration/phase7.sh   # ۴۲ تست: رد DLL/EXE/مسیر با hook واقعی، ترفندهای NTFS، حالت audit، هم‌زمانی
```

خروجی مورد انتظار:
```
RESULT: 37 passed, 0 failed     (phase4)
RESULT: 40 passed, 0 failed     (phase5)
RESULT: 23 passed, 0 failed     (phase6)
RESULT: 42 passed, 0 failed     (phase7)
```

اولین اجرای `tools/build.sh` یک image کوچک به نام `git-policy-build:go1.24` (Go + git) می‌سازد (فقط یک بار، نیاز به اینترنت).

باینری خروجی **static** است (حدود ۳ مگابایت) و روی هر لینوکس amd64 بدون هیچ وابستگی اجرا می‌شود.

---

## ۵. نوشتن سیاست (Policy)

### ۵.۱ ساختار کلی

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

نمونه‌ی کامل: `examples/policy.example.yaml`
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

```bash
B=dist/git-policy-0.1.0-linux-amd64
$B validate examples/policy.example.yaml          # خروجی متنی
$B validate --json examples/policy.example.yaml   # برای Jenkins
$B compile examples/policy.example.yaml           # دیدن نسخه‌ی نرمال‌شده (compiled.json)
```

کد خروج: `0` معتبر، `1` نامعتبر، `2` خطای ورودی/فایل.
نمونه‌ی خطا:
```
ERROR   V020  projects["finance/legacy-erp"].unblock_extensions: "dll" is blocked by mandatory and cannot be unblocked ...
RESULT: INVALID (1 errors, 0 warnings)
```

---

## ۶. نصب روی Lab

### ۶.۱ پیش از نصب (مهم)

- نصب **هیچ سرویسی را restart نمی‌کند** (نه GitLab، نه Gitaly، نه Docker).
- hook قدیمی `01-block-dll` **دست‌نخورده** می‌ماند.
- موتور در اولین نصب **خاموش (DISABLED)** است؛ یعنی نصب هیچ تغییری در رفتار push ایجاد نمی‌کند.
- اگر فایلی با نام `50-git-policy` از قبل با محتوای متفاوت وجود داشته باشد، نصب **متوقف** می‌شود (مگر با `--replace-hook` که اول backup می‌گیرد).

### ۶.۲ انتقال فایل‌ها به Docker host

```bash
# روی همین workstation:
cd ~/infra/git-policy
tools/build.sh all
scp -r dist install.sh uninstall.sh examples <user>@192.168.120.128:~/git-policy/
```

### ۶.۳ نصب (روی Docker host lab)

```bash
cd ~/git-policy

# ۱) فقط بررسی‌ها، بدون هیچ تغییری:
./install.sh --dry-run

# ۲) نصب واقعی:
./install.sh --actor "soroush"
```

بررسی‌هایی که `install.sh` انجام می‌دهد:
1. وجود باینری و تطابق sha256
2. در حال اجرا بودن کانتینر `gitlab`
3. mount بودن `/var/opt/gitlab` (پایدار بودن داده‌ها)
4. وجود کاربر `git`
5. وجود `custom_hooks_dir` در تنظیمات Gitaly و پوشه‌ی `pre-receive.d`
6. نمایش hookهای موجود
7. کپی باینری، بررسی مجدد sha256 داخل کانتینر، اجرای `admin install`، و نمایش `status`

خروجی مورد انتظار در انتها:
```
HEALTH: WARNING
  - WARNING: engine is disabled without expiry (initial install state)
  - WARNING: no loadable policy: no policy has been deployed
installed. Next: deploy a policy (admin apply) and enable it (admin enable).
```
(WARNING در این مرحله طبیعی است.)

### ۶.۴ استقرار سیاست و روشن کردن

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"

docker cp examples/policy.minimal.yaml gitlab:/tmp/policy.yaml
$GP admin apply   --actor soroush --reason "first policy" /tmp/policy.yaml
$GP admin enable  --actor soroush --reason "lab test"
$GP status
```

---

## ۷. عملیات روزمره

همه‌ی دستورات مدیریتی باید به صورت **root** داخل کانتینر اجرا شوند (`docker exec -u root`). در ادامه:
```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"
```

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

---

## ۸. ساختار روی سرور و مجوزها

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
```
remote: GL-HOOK-ERR: Push rejected by organizational Git policy.
remote: GL-HOOK-ERR: Rule: POLICY_UNAVAILABLE
remote: GL-HOOK-ERR: Required action: The Git policy service is unavailable. Contact the platform team.
```
جزئیات فنی (مسیر فایل، علت دقیق) **فقط در audit log** ثبت می‌شود، نه در پیام توسعه‌دهنده.

---

## ۱۰. شرایط اضطراری (Disaster Recovery)

### ۱۰.۱ سیاست جدید مشکل ایجاد کرده
```bash
$GP admin rollback --actor NAME --reason "rollback: rule X blocks valid pushes"
```

### ۱۰.۲ نیاز به توقف موقت موتور (موتور سالم است)
```bash
$GP admin disable --actor NAME --reason "incident INC-123" --ttl 1h
```

### ۱۰.۳ موتور کاملاً خراب است (باینری/state/سیاست) — Break-glass
این راه فقط با دسترسی root روی سرور ممکن است و **هر push** در حالت break-glass ثبت می‌شود:
```bash
# فعال‌سازی: همه‌ی pushها از git-policy عبور می‌کنند (hookهای دیگر مثل 01-block-dll همچنان اجرا می‌شوند)
docker exec -u root gitlab touch /var/opt/gitlab/git-policy/state/break-glass

# … رفع مشکل (install.sh دوباره، admin apply، admin rollback، …)

# غیرفعال‌سازی — فراموش نکنید! (status تا وقتی این فایل هست CRITICAL نشان می‌دهد)
docker exec -u root gitlab rm /var/opt/gitlab/git-policy/state/break-glass

# دیدن pushهایی که در این مدت عبور کرده‌اند:
docker exec gitlab cat /var/opt/gitlab/git-policy/logs/break-glass.log
```

### ۱۰.۴ فایل state خراب شده
```bash
$GP admin enable  --actor NAME --reason "repair state"      # یا:
$GP admin disable --actor NAME --reason "repair state" --ttl 1h
```
(هر دو فایل state را از نو و به صورت اتمیک می‌نویسند.)

### ۱۰.۵ نسخه‌های سیاست همه خراب شده‌اند
یک `admin apply` تازه همیشه ممکن است (حتی وقتی هیچ نسخه‌ای قابل خواندن نیست). `revision` را از آخرین مقدار بالاتر بگذارید.

### ۱۰.۶ hook تغییر کرده یا خراب شده
```bash
./install.sh --replace-hook     # نسخه‌ی فعلی در backup/ ذخیره می‌شود
```

### ۱۰.۷ حذف hook (بازگشت به وضعیت قبل)
```bash
./uninstall.sh
```
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

---

## ۱۳. فاز ۵: قوانین هویت (کاربر، گروه، پروژه)

### ۱۳.۱ چه چیزی اجرا می‌شود؟

برای **هر ref** در push (شاخه، تگ، حذف شاخه)، به این ترتیب:

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

- یک push چند-ref **همه یا هیچ** است: اگر حتی یک ref رد شود، هیچ refی ثبت نمی‌شود (رفتار خود Git).
- **حذف شاخه هم push است**؛ کاربری که `push: deny` دارد نمی‌تواند شاخه حذف کند.

### ۱۳.۲ مثال قوانین

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

جزئیات داخلی (کدام خط policy باعث رد شد) در پیام **نیست** و فقط در audit log ثبت می‌شود:

```json
{"action":"REJECT","user":"alex","project":"finance/payment-api","ref":"refs/heads/main",
 "rule":"USER_PUSH_DENIED","source":"users[\"alex\"].projects[\"finance/payment-api\"].push",
 "commit":"9447e19...","policy_version":"000001","membership":"fresh","timestamp":"2026-09-29T14:25:55+03:30"}
```

### ۱۳.۶ دستور explain: «چرا رد شد؟»

بدون push واقعی نشان می‌دهد policy درباره‌ی یک push فرضی چه تصمیمی می‌گیرد. در فاز ۱۱ همین به شکل اکشن `EXPLAIN` در Jenkins درمی‌آید.

```bash
git-policy explain --policy examples/policy.example.yaml \
    --user carol --groups contractors --project outsourcing/portal
```
```
Identity
  => namespace(depth 1)       group allow groups["contractors"].namespaces["outsourcing"].push
     global                   group deny  groups["contractors"].push
Verdict:     ALLOW
Effective content rules (enforced from Phases 7-8)
  blocked extensions: 7z (defaults), dll (MANDATORY), exe (MANDATORY), ...
  max file size:      20MiB (defaults.max_file_size)
```

- `=>` قانونی است که تصمیم را گرفته.
- بدون `--policy`، سیاست فعال سرور و cache عضویت خوانده می‌شوند.
- بخش «Effective content rules» نشان می‌دهد در فازهای ۷ و ۸ برای این پروژه و شاخه چه چیزی ممنوع خواهد بود.

### ۱۳.۷ تست روی lab (فاز ۵)

قوانین کاربری را می‌شود روی lab با کاربران واقعی تست کرد. قوانین گروهی تا فاز ۱۰ به cache نیاز دارند.

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

سپس:
- push با همان کاربر به همان پروژه → رد با `USER_PUSH_DENIED`
- push با همان کاربر به پروژه‌ی دیگر → قبول
- ویرایش یک فایل از Web UI گیت‌لب با همان کاربر → همان پیام در UI

```bash
docker exec gitlab sh -c 'grep REJECT /var/opt/gitlab/git-policy/logs/audit-*.jsonl | tail -n 2'
```

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

```yaml
exceptions:
  - id: EXC-IMPORT
    rules: [LIMIT_COMMITS]
    subjects: {users: [svc-import]}
    reason: import legacy history
    expires: 2026-10-31
```

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

```bash
cd /path/to/repo.git
echo "<old-sha> <new-sha> refs/heads/main" | git-policy scan --policy policy.yaml --project finance/app
```
```
commits=2 blobs=1 entries=1 exclusion-tips=1 duration=4ms
  refs/heads/main                55e85808ff46 100644 "Lib/Mic.Caching.dll"
```

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

- متن «Required action» از `settings.messages.remediation` در policy می‌آید. آدرس Nexus و روش کار با NuGet را همان‌جا بنویسید.
- Nexus فقط در متن پیام است. در دسترس نبودن Nexus **هیچ اثری** روی بررسی push ندارد.
- حداکثر ۲۰ مورد در پیام نشان داده می‌شود و حداکثر ۱۰۰۰ مورد در هر push ثبت می‌شود. بقیه با یک یادداشت «more findings» خلاصه می‌شوند.

### ۱۵.۴ رفع مشکل توسط توسعه‌دهنده

اگر DLL در یک commit قدیمی‌تر همین push اضافه شده باشد، حذف آن در commit بعدی **کافی نیست**، چون در تاریخچه می‌ماند. باید تاریخچه‌ی محلی بازنویسی شود:

```bash
git rebase -i origin/main          # commit مربوطه را edit کنید و فایل را حذف کنید
# یا:
git reset --soft origin/main && git rm --cached Lib/*.dll && git commit -m "..."
```

### ۱۵.۵ برنامه‌ی جایگزینی PoC روی lab (پیشنهادی)

```bash
GP="docker exec -u root gitlab /var/opt/gitlab/git-policy/bin/git-policy"
./install.sh --actor soroush                       # ارتقا به نسخه‌ی فاز ۷
```

**گام ۱: حالت audit.** git-policy فقط ثبت می‌کند و PoC همچنان رد می‌کند.

policy با `mandatory: {mode: audit, blocked_extensions: [dll, exe]}` و `settings: {mode: audit}` و یک revision جدید:
```bash
$GP admin apply  --actor soroush --reason "phase7 shadow" /tmp/p7.yaml
# چند push معمولی و یک push با DLL؛ سپس:
docker exec gitlab sh -c 'grep WOULD_REJECT /var/opt/gitlab/git-policy/logs/audit-*.jsonl | tail'
```

**گام ۲: حالت enforce.** اکنون هم PoC و هم git-policy رد می‌کنند.

همان policy با `mode: enforce` و revision بالاتر. push با DLL باید پیام `Rule: BLOCKED_EXTENSION` را نشان دهد. این را از CLI و از Web UI امتحان کنید.

**گام ۳: بازنشسته کردن PoC** (غیرمخرب؛ یک نسخه در `backup/` می‌ماند و در audit log ثبت می‌شود):
```bash
$GP admin retire-hook --actor soroush --name 01-block-dll
docker exec gitlab ls -l /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/    # فقط 50-git-policy
```
بعد از آن push با DLL باید **فقط** با پیام git-policy رد شود.

برای برگرداندن PoC (در صورت نیاز):
```bash
docker exec -u root gitlab sh -c 'cp /var/opt/gitlab/git-policy/backup/01-block-dll.retired.* /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll && chmod 0755 /var/opt/gitlab/gitaly/custom_hooks/pre-receive.d/01-block-dll'
```

### ۱۵.۶ رویدادهای جدید audit

| action | معنی |
|---|---|
| `REJECT` (با `file` و `commit`) | فایل ممنوع؛ push رد شد |
| `WOULD_REJECT` | در حالت audit: رد **می‌شد**، ولی push قبول شد |
| `EXCEPTION_APPLIED` (با `file`) | یک استثنا این فایل را مجاز کرد |
| `FINDINGS_TRUNCATED` | بیش از ۱۰۰۰ مورد در یک push |
| `HOOK_RETIRED` | یک hook دیگر (مثلاً PoC) به backup منتقل شد |

---

## ۱۶. واژه‌نامه

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
| **کلاس سیاست** | مجموعه‌ی refهایی که قوانین محتوایی یکسان دارند؛ برای تعیین «قبلاً بررسی‌شده» استفاده می‌شود |
