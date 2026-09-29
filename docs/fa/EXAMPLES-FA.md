# ۱۰ مثال git-policy، از ساده تا کامل

هر مثال یک فایل policy آماده در `examples/scenarios/` است. **هر ردیف از جدول‌های این سند با یک `git push` واقعی تست شده است** (۶۶ مورد، همه موفق):

```bash
tools/build.sh build
tests/examples/verify.sh                 # همه‌ی ۱۰ مثال → RESULT: 66 passed, 0 failed
tests/examples/verify.sh "" 06 07        # فقط مثال‌های ۶ و ۷
```

برای امتحان هر مثال بدون push:

```bash
B=dist/git-policy-0.1.0-linux-amd64
$B validate examples/scenarios/05-namespaces-projects.yaml
$B explain  --policy examples/scenarios/05-namespaces-projects.yaml --user dev --project finance/accounting
```

| # | مثال | مفهوم اصلی | سطح |
|---|---|---|---|
| ۱ | فقط DLL ممنوع | `mandatory` | ⭐ |
| ۲ | چند نوع باینری + پیام Nexus | `mandatory` در برابر `defaults`، پیام سفارشی | ⭐ |
| ۳ | محدودیت حجم فایل | `max_file_size`، سقف سازمانی | ⭐⭐ |
| ۴ | rollout امن (حالت audit) | `mode: audit`، `WOULD_REJECT` | ⭐⭐ |
| ۵ | قوانین واحد و پروژه | `namespaces`، `projects`، `unblock`، `enabled: false`، `blocked_paths` | ⭐⭐⭐ |
| ۶ | قوانین کاربر | `users`، دقیق‌ترین سطح، ref، `@unknown` | ⭐⭐⭐ |
| ۷ | قوانین گروه | `groups`، cache عضویت، کاربر در برابر گروه | ⭐⭐⭐⭐ |
| ۸ | شاخه‌های release و تگ‌ها | `refs`، امضای PE | ⭐⭐⭐⭐ |
| ۹ | استثناها | `exceptions` با مسیر، حجم و محدودیت | ⭐⭐⭐⭐ |
| ۱۰ | policy کامل سازمانی | همه با هم | ⭐⭐⭐⭐⭐ |

> **یادآوری:** همه‌ی این قوانین روی **همه‌ی commitهای جدید** یک push اجرا می‌شوند، نه فقط آخرین commit. DLLی که در یک commit اضافه و در commit بعدی حذف شود هم رد می‌شود.

---

## مثال ۱: فقط DLL ممنوع ⭐

**هدف:** همان کاری که PoC فعلی (`01-block-dll`) انجام می‌دهد، ولی با موتور کامل.

```yaml
apiVersion: git-policy/v1
kind: GitPolicy
metadata:
  name: block-dll-only
  revision: 1
mandatory:
  blocked_extensions: [dll]
```

**توضیح:**
- `apiVersion`، `kind` و `metadata` در هر policy لازم‌اند. `revision` با هر تغییر باید بزرگ‌تر شود.
- `mandatory` یعنی **کف امنیتی**: هیچ پروژه، namespace یا شاخه‌ای نمی‌تواند آن را حذف کند.
- پسوند بدون حساسیت به بزرگی حروف است، و ترفندهای ویندوزی مثل نقطه‌ی انتهایی هم شناسایی می‌شوند.

| push | نتیجه | چرا |
|---|---|---|
| `src/Program.cs` | ✅ قبول | پسوند مجاز |
| `Lib/Mic.Caching.dll` | ❌ `BLOCKED_EXTENSION` | dll ممنوع |
| `Lib/TEST.DLL` | ❌ `BLOCKED_EXTENSION` | حروف بزرگ فرقی ندارد |
| `Lib/evil.dll.` (نقطه در انتها) | ❌ `BLOCKED_EXTENSION` | ویندوز آن را `evil.dll` می‌بیند |
| `tools/setup.exe` | ✅ قبول | در این policy فقط dll ممنوع است |

---

## مثال ۲: چند نوع باینری + پیام راهنمای Nexus ⭐

**هدف:** چند نوع فایل باینری را ممنوع کنیم و به توسعه‌دهنده بگوییم **چه کار کند**.

```yaml
settings:
  messages:
    header: "Push rejected by the organizational Git policy."
    support: "Help: #devops-help"
    remediation:
      BLOCKED_EXTENSION: >-
        Binary dependencies do not belong in Git. Publish them as NuGet packages
        to Nexus (nuget-hosted) and reference them with PackageReference.
mandatory:
  blocked_extensions: [dll, exe, pdb, msi]      # هرگز، هیچ‌جا
defaults:
  blocked_extensions: [zip, rar, 7z, iso, nupkg] # پیش‌فرض سازمان؛ پروژه‌ها می‌توانند باز کنند
```

**توضیح:**
- **`mandatory` در برابر `defaults`:** هر دو ممنوع می‌کنند، ولی `defaults` را یک پروژه یا namespace می‌تواند با `unblock_extensions` باز کند (مثال ۵). `mandatory` را هرگز نمی‌تواند.
- **`remediation`:** متن «Required action» در پیام. اینجا جای آدرس Nexus و روش NuGet است.
- در دسترس نبودن Nexus هیچ اثری روی بررسی push ندارد؛ Nexus فقط در متن پیام است.

| push | نتیجه |
|---|---|
| `installer/setup.msi` | ❌ `BLOCKED_EXTENSION` |
| `bin/app.pdb` | ❌ `BLOCKED_EXTENSION` |
| `deps/tools.7z` | ❌ `BLOCKED_EXTENSION` |
| `pkgs/lib.1.0.nupkg` | ❌ `BLOCKED_EXTENSION` |
| `docs/manual.pdf` | ✅ قبول |

پیام واقعی‌ای که توسعه‌دهنده می‌بیند:

```
remote: GL-HOOK-ERR: Push rejected by the organizational Git policy.
remote: GL-HOOK-ERR: User: alex
remote: GL-HOOK-ERR: Project: finance/payment-api
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Rule: BLOCKED_EXTENSION
remote: GL-HOOK-ERR: Ref: refs/heads/main
remote: GL-HOOK-ERR: Commit: 97bb29d5d544663c453d3c22fbf2997a95078c3c
remote: GL-HOOK-ERR: File: Lib/Mic.Caching.dll
remote: GL-HOOK-ERR: Blocked extension: .dll
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Required action (BLOCKED_EXTENSION): Binary dependencies do not belong in Git. Publish them as NuGet packages to Nexus (nuget-hosted) and reference them with PackageReference.
remote: GL-HOOK-ERR: Help: #devops-help
```

---

## مثال ۳: محدودیت حجم فایل ⭐⭐

**هدف:** جلوی فایل‌های بزرگ را بگیریم، و یک پروژه‌ی طراحی اجازه‌ی بیشتری داشته باشد.

```yaml
mandatory:
  max_file_size: 50MiB        # سقف سازمانی: هیچ‌چیز نمی‌تواند از آن بالاتر برود
defaults:
  max_file_size: 10MiB        # پروژه‌های معمولی
projects:
  design/assets:
    max_file_size: 40MiB      # این پروژه تا 40MiB، ولی هرگز بالای 50MiB
```

**توضیح:**
- واحدها باینری‌اند: `KiB`، `MiB`، `GiB`. واحد `MB` قبول نمی‌شود تا ابهام ۱۰۰۰ و ۱۰۲۴ پیش نیاید.
- **دقیق‌ترین سطح برنده است**: پروژه > namespace > defaults. ولی نتیجه همیشه زیر سقف `mandatory` می‌ماند.
- اگر پروژه‌ای بخواهد `max_file_size: 100MiB` بگذارد، validator آن را با خطای `V021` **رد می‌کند**.
- حجم از header شیء Git خوانده می‌شود؛ هیچ checkoutی انجام نمی‌شود.

| پروژه | فایل | نتیجه | حد مؤثر |
|---|---|---|---|
| `team/app` | ۵ MiB | ✅ قبول | 10MiB (defaults) |
| `team/app` | ۱۲ MiB | ❌ `FILE_TOO_LARGE` | 10MiB |
| `design/assets` | ۳۰ MiB | ✅ قبول | 40MiB (پروژه) |
| `design/assets` | ۴۵ MiB | ❌ `FILE_TOO_LARGE` | 40MiB |

---

## مثال ۴: rollout امن با حالت audit ⭐⭐

**هدف:** روی repositoryهایی که از قبل پر از DLL هستند، قبل از فعال کردن واقعی ببینیم **چه چیزی رد می‌شد**.

```yaml
settings:
  mode: audit                 # قوانین غیر mandatory: فقط ثبت
mandatory:
  mode: audit                 # قوانین mandatory: فقط ثبت
  blocked_extensions: [dll, exe]
defaults:
  max_file_size: 10MiB
```

**توضیح:**
- در حالت `audit`، push **قبول می‌شود** ولی در audit log یک رویداد `WOULD_REJECT` با نام فایل، کاربر و پروژه ثبت می‌شود.
- حالت `mandatory` جدا از بقیه تنظیم می‌شود، پس می‌شود مثلاً قوانین mandatory را enforce و بقیه را audit گذاشت.
- **برنامه‌ی پیشنهادی:** ۱ تا ۲ هفته audit، بعد بررسی لاگ‌ها و مهاجرت پروژه‌ها به NuGet، و در آخر تغییر به `enforce` (فقط `mode` و `revision` عوض می‌شود).

| push | نتیجه | در audit log |
|---|---|---|
| `Lib/Mic.Caching.dll` | ✅ قبول | `WOULD_REJECT` / `BLOCKED_EXTENSION` |
| فایل ۱۲ MiB | ✅ قبول | `WOULD_REJECT` / `FILE_TOO_LARGE` |
| `src/Program.cs` | ✅ قبول | — |

دیدن نتیجه:
```bash
grep WOULD_REJECT /var/opt/gitlab/git-policy/logs/audit-*.jsonl
```

---

## مثال ۵: قوانین مخصوص یک واحد و پروژه ⭐⭐⭐

**هدف:** واحد مالی قوانین سخت‌تری داشته باشد، یک پروژه‌ی خاص استثنای سبکی بگیرد، و یک پروژه‌ی آزمایشی فقط قوانین پایه را داشته باشد.

```yaml
mandatory:
  blocked_extensions: [dll, exe]
defaults:
  blocked_extensions: [zip]
namespaces:
  finance:                                    # finance/* و همه‌ی زیرگروه‌ها
    blocked_extensions: [pdb]
    blocked_paths: ["**/bin/Debug/**", "**/bin/Release/**", "**/obj/**"]
  finance/legacy:
    mode: audit                               # پروژه‌های قدیمی: فقط ثبت
projects:
  finance/reporting:
    unblock_extensions: [zip]                 # این پروژه قالب‌های zip دارد
  sandbox/playground:
    enabled: false                            # فقط قوانین mandatory
```

**توضیح:**
- **لیست‌ها جمع می‌شوند:** برای `finance/accounting` ممنوع‌ها می‌شوند `dll, exe` (mandatory) به‌علاوه‌ی `zip` (defaults) به‌علاوه‌ی `pdb` (finance).
- **`blocked_paths`** الگوی مسیر است: `**` یعنی هر عمقی، `*` یعنی داخل یک پوشه. پوشه‌های خروجی build (`bin/Debug`، `obj`) نباید commit شوند.
- **`unblock_extensions`** فقط چیزی را باز می‌کند که یک سطح بالاتر بسته باشد. باز کردن `dll` (که mandatory است) با خطای `V020` **رد می‌شود**.
- **`enabled: false`** همه‌ی قوانین غیر mandatory را برای آن پروژه خاموش می‌کند، ولی کف امنیتی سر جایش می‌ماند.
- namespace بر اساس **بخش‌های مسیر** مقایسه می‌شود: `finance/legacy` شامل `finance/legacy-erp` **نمی‌شود**.

| پروژه | فایل | نتیجه | چرا |
|---|---|---|---|
| `finance/accounting` | `bin/app.pdb` | ❌ `BLOCKED_EXTENSION` | pdb در finance ممنوع |
| `marketing/site` | `bin/app.pdb` | ✅ قبول | خارج از finance |
| `finance/accounting` | `src/obj/Debug/app.cache` | ❌ `BLOCKED_PATH` | الگوی `**/obj/**` |
| `finance/reporting` | `templates/q3.zip` | ✅ قبول | zip برای این پروژه باز شده |
| `finance/accounting` | `templates/q3.zip` | ❌ `BLOCKED_EXTENSION` | zip در defaults ممنوع |
| `finance/legacy/billing` | `bin/old.pdb` | ✅ قبول + `WOULD_REJECT` | حالت audit |
| `finance/legacy/billing` | `bin/old.dll` | ❌ `BLOCKED_EXTENSION` | mandatory همیشه enforce است |
| `sandbox/playground` | `test.zip` | ✅ قبول | `enabled: false` |
| `sandbox/playground` | `test.exe` | ❌ `BLOCKED_EXTENSION` | mandatory حتی اینجا |

---

## مثال ۶: قوانین کاربر ⭐⭐⭐

**هدف:** دسترسی push چند کاربر مشخص را محدود کنیم، علاوه بر مجوزهای خود GitLab.

```yaml
mandatory:
  deny_users: ["@unknown", ex.employee]       # @unknown = push بدون نام کاربری
users:
  alex:
    projects:
      finance/payment-api: {push: deny}       # alex: همه‌جا به‌جز payment-api
  intern1:
    push: deny                                # intern1: هیچ‌جا …
    namespaces:
      training: {push: allow}                 # … به‌جز training/* (سطح دقیق‌تر برنده است)
  bob:
    projects:
      finance/payment-api:
        refs: {"refs/heads/main": deny}       # bob: payment-api بله، ولی نه main
```

**توضیح:**
- git-policy **جایگزین** مجوزهای GitLab نیست. اگر GitLab اجازه ندهد، push اصلاً به hook نمی‌رسد. git-policy فقط **محدودیت اضافه** می‌گذارد.
- **دقیق‌ترین سطح تصمیم می‌گیرد:** `intern1` در سطح global رد و در سطح namespace `training` مجاز است؛ namespace دقیق‌تر است، پس در `training/*` مجاز است.
- **ref:** قانون می‌تواند فقط برای یک شاخه باشد (`refs/heads/main`). برای جلوگیری کلی از push مستقیم به main، **Protected Branches خود GitLab** ابزار درست است؛ این قانون برای یک کاربر خاص است.
- **حذف شاخه هم push حساب می‌شود.** کسی که `push: deny` دارد نمی‌تواند شاخه حذف کند.
- **`@unknown`:** pushی که GitLab نام کاربری برایش نفرستاده باشد.

| کاربر | پروژه / شاخه | نتیجه |
|---|---|---|
| alex | `finance/payment-api` feature/x | ❌ `USER_PUSH_DENIED` |
| alex | `finance/reporting` main | ✅ قبول |
| intern1 | `finance/app` | ❌ `USER_PUSH_DENIED` |
| intern1 | `training/lab1` | ✅ قبول |
| bob | `finance/payment-api` main | ❌ `USER_PUSH_DENIED` |
| bob | `finance/payment-api` feature/x | ✅ قبول |
| ex.employee | هر جا | ❌ `MANDATORY_USER_DENIED` |
| (بدون نام کاربری) | هر جا | ❌ `MANDATORY_USER_DENIED` |

---

## مثال ۷: قوانین گروه‌های GitLab ⭐⭐⭐⭐

**هدف:** قانون بر اساس عضویت در گروه‌های GitLab، مثلاً پیمانکاران فقط در یک namespace.

```yaml
settings:
  membership:
    soft_max_age: 2h
    hard_max_age: 24h
    on_unavailable: deny_if_group_rules
mandatory:
  deny_groups: [offboarding]                  # اعضای offboarding: هرگز
groups:
  contractors:
    push: deny                                # پیمانکاران: هیچ‌جا …
    namespaces:
      outsourcing: {push: allow}              # … به‌جز outsourcing/*
  qa:
    projects:
      finance/payment-api:
        refs: {"refs/heads/release/**": deny} # QA روی شاخه‌های release push نکند
users:
  carol:                                      # carol پیمانکار است، ولی صریحاً
    namespaces:                               # در finance مجاز است
      finance: {push: allow}
```

**توضیح:**
- **عضویت گروه از GitLab API در لحظه‌ی push خوانده نمی‌شود.** از فایل محلی `membership/current.json` خوانده می‌شود که Jenkins به‌طور دوره‌ای به‌روز می‌کند (فاز ۱۰). push هرگز منتظر شبکه نمی‌ماند.
- **cache کهنه فقط محدود می‌کند:** اگر cache بیش از ۲۴ ساعت عمر داشته باشد، allowهای گروهی نادیده گرفته می‌شوند و denyها می‌مانند.
- **cache وجود ندارد و policy deny گروهی دارد:** همه‌ی pushها رد می‌شوند (`MEMBERSHIP_UNAVAILABLE`). به همین دلیل `admin enable` در این وضعیت اصلاً اجرا نمی‌شود.
- **کاربر در برابر گروه:** carol عضو contractors است، ولی قانون **کاربری** او در سطح namespace `finance` دقیق‌تر از قانون **گروهی** global است، پس در finance مجاز است. در `hr/app` فقط قانون گروه اعمال می‌شود و رد می‌شود.

| کاربر [گروه] | پروژه / شاخه | نتیجه |
|---|---|---|
| dave [contractors] | `outsourcing/portal` | ✅ قبول |
| dave [contractors] | `finance/app` | ❌ `GROUP_PUSH_DENIED` |
| carol [contractors] | `finance/app` | ✅ قبول (قانون کاربری دقیق‌تر) |
| carol [contractors] | `hr/app` | ❌ `GROUP_PUSH_DENIED` |
| tina [qa] | `finance/payment-api` release/1.0 | ❌ `GROUP_PUSH_DENIED` |
| tina [qa] | `finance/payment-api` main | ✅ قبول |
| mallory [offboarding] | هر جا | ❌ `MANDATORY_GROUP_DENIED` |

دستور `explain` دقیقاً نشان می‌دهد چرا carol مجاز است (خروجی واقعی):

```
$ git-policy explain --policy examples/scenarios/07-groups.yaml --user carol --groups contractors --project finance/app
Identity
  => namespace(depth 1)       user  allow users["carol"].namespaces["finance"].push
     global                   group deny  groups["contractors"].push
Verdict:     ALLOW
```

---

## مثال ۸: شاخه‌های release و تگ‌های نسخه ⭐⭐⭐⭐

**هدف:** روی کدی که منتشر می‌شود قوانین سخت‌تری داشته باشیم.

```yaml
mandatory:
  blocked_extensions: [dll, exe]
  max_file_size: 50MiB
defaults:
  max_file_size: 20MiB
  refs:
    "refs/heads/release/**":
      max_file_size: 5MiB
      blocked_signatures: [pe]      # فایل اجرایی ویندوز از روی محتوا، با هر نامی
    "refs/tags/v*":
      max_file_size: 5MiB
      blocked_signatures: [pe]
```

**توضیح:**
- **الگوی ref** کامل نوشته می‌شود: `refs/heads/...` برای شاخه و `refs/tags/...` برای تگ. `*` داخل یک بخش است و `**` چند بخش (`release/**` شامل `release/1.0/hotfix` هم می‌شود).
- **`blocked_signatures: [pe]`:** اگر کسی `Mic.Caching.dll` را به `readme.txt` تغییر نام دهد، پسوند چیزی نشان نمی‌دهد، ولی **header فایل** (`MZ` … `PE\0\0`) آن را لو می‌دهد. چون خواندن محتوا هزینه دارد، فقط روی شاخه‌های مهم فعال شده است.
- **اولین شاخه‌ی release** تاریخچه‌ای را که هرگز با قوانین release بررسی نشده **دوباره بررسی می‌کند**. این جلوی ترفند «push develop به release» را می‌گیرد.

| شاخه / تگ | فایل | نتیجه |
|---|---|---|
| main | فایل ۸ MiB | ✅ قبول (حد 20MiB) |
| release/1.0 | فایل ۸ MiB | ❌ `FILE_TOO_LARGE` (حد 5MiB) |
| release/1.0 | `docs/readme.txt` که در واقع PE است | ❌ `BLOCKED_SIGNATURE` |
| feature/x | همان `readme.txt` | ✅ قبول (قانون امضا ندارد) |
| تگ v1.0 | همان `readme.txt` | ❌ `BLOCKED_SIGNATURE` |

---

## مثال ۹: استثناهای کنترل‌شده ⭐⭐⭐⭐

**هدف:** برای موارد واقعی و موقت استثنا بدهیم، ولی **محدود، دلیل‌دار و تاریخ‌دار**.

```yaml
settings:
  limits: {max_new_commits: 1000}
  exceptions: {max_lifetime_days: 180}
mandatory:
  blocked_extensions: [dll, exe]
  max_file_size: 50MiB
defaults:
  max_file_size: 10MiB
exceptions:
  - id: EXC-2026-001                          # یک SDK مشخص، یک پروژه، فقط main
    rules: [BLOCKED_EXTENSION]
    mandatory: true                           # چون dll در mandatory است
    scope:
      projects: [finance/legacy-erp]
      refs: ["refs/heads/main"]
      paths: ["vendor/VendorSdk/*.dll"]
    reason: Vendor SDK has no NuGet package yet
    ticket: INFRA-2231
    approved_by: security-team
    expires: 2026-12-31
  - id: EXC-2026-002                          # یک کاربر، مدل‌ها تا 80MiB
    rules: [FILE_TOO_LARGE]
    mandatory: true                           # چون 80MiB بالای سقف 50MiB است
    subjects: {users: [ds1]}
    scope: {projects: [analytics/forecast]}
    max_file_size: 80MiB
    reason: ML model snapshots until the artifact store is ready
    expires: 2026-12-15
  - id: EXC-2026-003                          # import تاریخچه از محدودیت commit عبور کند
    rules: [LIMIT_COMMITS]
    subjects: {users: [svc-migration]}
    scope: {namespaces: [legacy]}
    reason: One-time import of the SVN history
    expires: 2026-10-31
```

**توضیح:**
- **استثنا صریح است:** باید بگوید **کدام قانون** (`rules`)، **برای چه کسی** (`subjects`)، **کجا** (`scope`: پروژه، namespace، شاخه، مسیر)، **چرا** (`reason`) و **تا کی** (`expires`).
- **چیزهایی که validator رد می‌کند:**
  - `rules: ["*"]` یا `bypass_all`
  - استثنای «همه‌ی کاربران، همه‌جا» (`V031`)
  - تاریخ انقضای بیش از `max_lifetime_days` (`V032`)
  - `paths` روی قانونی که مسیر ندارد (`V033`)
- **عبور از mandatory** فقط با `mandatory: true` ممکن است.
- **`max_file_size` در استثنا** «تا این حد» است، نه «نامحدود»: مدل ۹۰ MiB هنوز رد می‌شود.
- هر بار که استثنا استفاده شود، رویداد `EXCEPTION_APPLIED` با فایل و کاربر ثبت می‌شود. استثنای منقضی‌شده خودبه‌خود بی‌اثر می‌شود.

| کاربر | پروژه / شاخه | push | نتیجه |
|---|---|---|---|
| dev | `finance/legacy-erp` main | `vendor/VendorSdk/Sdk.dll` | ✅ قبول (EXC-2026-001) |
| dev | `finance/legacy-erp` dev | همان فایل | ❌ `BLOCKED_EXTENSION` (شاخه‌ی دیگر) |
| dev | `finance/legacy-erp` main | `vendor/Other/x.dll` | ❌ `BLOCKED_EXTENSION` (مسیر دیگر) |
| ds1 | `analytics/forecast` | مدل ۶۰ MiB | ✅ قبول (EXC-2026-002) |
| ds1 | `analytics/forecast` | مدل ۹۰ MiB | ❌ `FILE_TOO_LARGE` (بیشتر از 80MiB) |
| dev | `analytics/forecast` | مدل ۶۰ MiB | ❌ `FILE_TOO_LARGE` (کاربر دیگر) |
| svc-migration | `legacy/erp` | ۱۲۰۰ commit | ✅ قبول (EXC-2026-003) |
| dev | `legacy/erp` | ۱۲۰۰ commit | ❌ `LIMIT_COMMITS` |

---

## مثال ۱۰: policy کامل سازمانی ⭐⭐⭐⭐⭐

**هدف:** یک policy واقعی تولیدی که همه‌ی مفاهیم را با هم دارد. فایل کامل: `examples/scenarios/10-production.yaml`.

```yaml
settings:
  mode: enforce
  repository_types: {project: all, wiki: mandatory_only, snippet: mandatory_only, design: none}
  limits: {max_ref_updates: 500, max_new_commits: 20000, max_new_blobs: 200000, evaluation_timeout: 45s}
  membership: {soft_max_age: 2h, hard_max_age: 24h, on_unavailable: deny_if_group_rules}
  audit: {retention_days: 365}
  exceptions: {max_lifetime_days: 120}
  messages: { ... پیام‌های Nexus برای هر قانون ... }
mandatory:
  blocked_extensions: [dll, exe, msi, sys]
  max_file_size: 100MiB
  deny_users: [ex.employee]
  deny_groups: [offboarding]
defaults:
  blocked_extensions: [pdb, nupkg, zip, rar, 7z, iso]
  blocked_paths: ["**/bin/Debug/**", "**/bin/Release/**", "**/obj/**"]
  max_file_size: 20MiB
  refs:
    "refs/heads/release/**": {blocked_signatures: [pe], max_file_size: 10MiB}
namespaces:
  finance:        {max_file_size: 10MiB}
  finance/legacy: {mode: audit}
projects:
  design/assets:      {max_file_size: 80MiB}
  sandbox/playground: {enabled: false}
users:
  intern1: {push: deny, namespaces: {training: {push: allow}}}
groups:
  contractors: {push: deny, namespaces: {outsourcing: {push: allow}}}
exceptions:
  - id: EXC-ERP-SDK   # SDK فروشنده، فقط main پروژه‌ی legacy-erp
    ...
```

**توضیح بخش‌های جدید:**
- **`repository_types`:**
  - wikiها و snippetها فقط قوانین mandatory را می‌گیرند.
  - design repositoryها (تصاویر طراحی که GitLab با LFS ذخیره می‌کند) بررسی نمی‌شوند.
- **`limits`:** محافظت در برابر pushهای عمداً سنگین. همه fail-closed هستند.
- **`audit.retention_days`:** مدت نگهداری لاگ‌ها.
- **`exceptions.max_lifetime_days: 120`:** هیچ استثنایی بیش از ۴ ماه عمر نمی‌کند.

| کاربر | پروژه / شاخه | push | نتیجه |
|---|---|---|---|
| dev | `team/app` main | `src/Program.cs` | ✅ قبول |
| dev | `team/app` main | `Lib/Mic.Caching.dll` | ❌ `BLOCKED_EXTENSION` |
| dev | `team/app` main | `src/obj/Release/app.cache` | ❌ `BLOCKED_PATH` |
| dev | `finance/accounting` main | فایل ۱۲ MiB | ❌ `FILE_TOO_LARGE` (finance: 10MiB) |
| dev | `design/assets` main | ویدیو ۶۰ MiB | ✅ قبول (پروژه: 80MiB) |
| dev | `team/app` release/2.0 | `readme.txt` که PE است | ❌ `BLOCKED_SIGNATURE` |
| dev | `finance/legacy/billing` | `build/app.pdb` | ✅ قبول + `WOULD_REJECT` |
| dev | `finance/legacy-erp` main | `vendor/VendorSdk/Sdk.dll` | ✅ قبول (استثنا) |
| intern1 | `team/app` | هر چیزی | ❌ `USER_PUSH_DENIED` |
| dave [contractors] | `team/app` | هر چیزی | ❌ `GROUP_PUSH_DENIED` |
| dave [contractors] | `outsourcing/portal` | هر چیزی | ✅ قبول |
| ex.employee | هر جا | هر چیزی | ❌ `MANDATORY_USER_DENIED` |

---

## جمع‌بندی: از کجا شروع کنیم؟

1. **هفته‌ی اول:** مثال ۱ یا ۲ در **حالت audit** (مثال ۴). PoC فعلی همچنان رد می‌کند.
2. **بررسی لاگ‌ها:** `grep WOULD_REJECT` نشان می‌دهد چه پروژه‌هایی DLL دارند. آن‌ها به NuGet/Nexus مهاجرت کنند یا استثنای موقت بگیرند (مثال ۹).
3. **enforce** و بازنشسته کردن PoC (`admin retire-hook`).
4. **به‌تدریج:** محدودیت حجم (۳)، قوانین واحدها (۵)، شاخه‌های release (۸).
5. **در آخر:** قوانین کاربر و گروه (۶ و ۷). قوانین گروهی به همگام‌سازی عضویت از Jenkins (فاز ۱۰) نیاز دارند.

> تاریخ `expires` در مثال‌ها ثابت نوشته شده است. قبل از استفاده، آن را به تاریخی حداکثر `max_lifetime_days` روز بعد از امروز تغییر دهید. اسکریپت تست این کار را خودکار انجام می‌دهد.
