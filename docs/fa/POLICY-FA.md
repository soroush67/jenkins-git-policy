<div dir="rtl">

# راهنمای نوشتن policy، قدم به قدم

این راهنما برای کسی است که باید **policy بنویسد یا تغییر دهد**. از یک فایل خالی شروع می‌کنیم و قدم به قدم به یک policy کامل سازمانی می‌رسیم. در انتهای راهنما همه‌ی کلیدها، مقدارهای مجاز، قواعد اولویت و کدهای خطا به‌صورت مرجع آمده‌اند.

> فایل هر قدم آماده و **تست‌شده** در پوشه‌ی [`examples/tutorial/`](../../examples/tutorial/) است (`step1-minimal.yaml` تا `step9-full.yaml`).
> همه‌ی خروجی‌هایی که در این راهنما می‌بینید خروجی واقعی برنامه روی همین فایل‌ها هستند.
>
> اسناد دیگر: [GUIDE-FA.md](GUIDE-FA.md) (راهنمای کلی و عملیات) · [CLI-FA.md](CLI-FA.md) (همه‌ی دستورات) · [EXAMPLES-FA.md](EXAMPLES-FA.md) (۱۰ سناریو)

---

## ۰. قبل از شروع

### policy کجاست؟

policy یک فایل YAML در repository گیت‌لب `platform/git-policy-config` است (GitOps):

| فایل | کجا اعمال می‌شود | چطور |
|---|---|---|
| `test/policy.yaml` | GitLab محیط TEST | خودکار، چند دقیقه بعد از merge در `main` (job `git-policy-gitops`) |
| `production/policy.yaml` | GitLab محیط PRODUCTION | دستی از Jenkins، با تأیید نفر دوم |

**هیچ‌وقت policy را مستقیم روی سرور تغییر ندهید.** همیشه در git تغییر دهید؛ تاریخچه، MR و rollback از همین‌جا می‌آید.

### ابزارها

| ابزار | کاربرد |
|---|---|
| **ادیتور + schema** | در VS Code افزونه‌ی YAML (Red Hat) را نصب کنید. خط اول فایل `# yaml-language-server: $schema=…/policy.v1.schema.json` باعث می‌شود ادیتور کلیدها را پیشنهاد دهد و غلط املایی را همان لحظه نشان دهد. فایل schema در `schema/policy.v1.schema.json` همین پروژه است |
| **`git-policy validate`** | بررسی کامل policy (مرجع نهایی همین است، نه schema) |
| **`git-policy explain`** | «این policy برای فلان کاربر و فلان پروژه چه تصمیمی می‌گیرد؟» بدون push واقعی |
| **Jenkins → `git-policy`، `ACTION=VALIDATE_POLICY`** | همان validate روی **شاخه‌ی MR شما** (`POLICY_REF=نام-شاخه`)، بدون نیاز به نصب چیزی |

باینری `git-policy` روی لینوکس یا WSL اجرا می‌شود و به چیز دیگری نیاز ندارد:

<div dir="ltr">

```bash
B=dist/git-policy-0.1.0-linux-amd64       # یا هر جایی که باینری را گذاشته‌اید
$B validate test/policy.yaml
```

</div>

### چرخه‌ی کار

<div dir="ltr">

```
 شاخه‌ی جدید ──> ویرایش policy.yaml + بالا بردن metadata.revision
      │
      ▼
 validate و explain (محلی، یا Jenkins VALIDATE_POLICY با POLICY_REF=شاخه)
      │
      ▼
 Merge Request ──> بازبینی ──> merge در main
      │
      ▼
 job git-policy-gitops: validate + اعمال خودکار test/policy.yaml روی TEST
      │  (بررسی روی TEST)
      ▼
 job git-policy: ACTION=UPDATE_POLICY, ENVIRONMENT=PRODUCTION ──> تأیید نفر دوم ──> اعمال
```

</div>

---

## ۱. قدم ۱: کوچک‌ترین policy معتبر

فایل: [`step1-minimal.yaml`](../../examples/tutorial/step1-minimal.yaml)

<div dir="ltr">

```yaml
# yaml-language-server: $schema=../../schema/policy.v1.schema.json
apiVersion: git-policy/v1
kind: GitPolicy
metadata:
  name: acme-git-policy
  revision: 1
  description: Git content and identity policy of ACME.
```

</div>

| کلید | الزامی | توضیح |
|---|---|---|
| `apiVersion` | بله | همیشه `git-policy/v1` (نسخه‌ی قالب فایل؛ ربطی به نسخه‌ی نرم‌افزار ندارد) |
| `kind` | بله | همیشه `GitPolicy` |
| `metadata.name` | بله | نام policy؛ فقط حروف **کوچک** انگلیسی، عدد و `-`، مثلاً `acme-git-policy` |
| `metadata.revision` | بله | عدد صحیح ≥ ۱. **در هر تغییر باید بزرگ‌تر شود**؛ سرور revision برابر یا کمتر را رد می‌کند (`V040`) |
| `metadata.description` | خیر | توضیح آزاد، حداکثر ۱۰۰۰ کاراکتر |

این policy هنوز هیچ چیزی را منع نمی‌کند، ولی معتبر است:

<div dir="ltr">

```
$ git-policy validate step1-minimal.yaml
git-policy validate: step1-minimal.yaml
  policy:   acme-git-policy (revision 1)
  sha256:   34c696a1...
RESULT: VALID (0 errors, 0 warnings)
```

</div>

> **قانون طلایی:** هر بار که فایل را تغییر می‌دهید، `revision` را یکی بالا ببرید. در ادامه‌ی این راهنما هم همین کار را کرده‌ایم (revision قدم ۲ برابر ۲ است و الی آخر).

---

## ۲. قدم ۲: کف امنیتی (`mandatory`)

فایل: [`step2-mandatory.yaml`](../../examples/tutorial/step2-mandatory.yaml)

`mandatory` قوانینی است که در **همه‌ی** پروژه‌ها، شاخه‌ها و برای **همه‌ی** کاربران اجرا می‌شود و **هیچ سطح پایین‌تری نمی‌تواند آن را ضعیف کند**. تنها راه عبور از آن یک استثنای صریح با `mandatory: true` است (قدم ۸).

<div dir="ltr">

```yaml
mandatory:
  blocked_extensions: [dll, exe]      # by name, at any depth, any case
  blocked_signatures: [pe]            # Windows executables even if renamed (e.g. lib.txt)
  max_file_size: 50MiB                # absolute cap for every file
  deny_users: [terminated.user]       # these accounts cannot push anywhere
```

</div>

| کلید | مقدار | معنی |
|---|---|---|
| `blocked_extensions` | فهرست پسوند، بدون نقطه | فایل با این پسوند در هیچ عمقی پذیرفته نمی‌شود. بزرگی/کوچکی حروف مهم نیست (`X.DLL` هم رد می‌شود). ترفندهای ویندوزی مثل `a.dll.` یا `a.dll::$DATA` هم شناخته می‌شوند |
| `blocked_paths` | فهرست الگوی مسیر | مثل بالا، ولی بر اساس مسیر (بخش ۱۴) |
| `blocked_signatures` | فقط `pe` | محتوای فایل را نگاه می‌کند: هر فایل اجرایی ویندوز (DLL، EXE، …) **حتی اگر نامش را عوض کرده باشند** |
| `max_file_size` | اندازه، مثل `50MiB` | سقف مطلق اندازه‌ی هر فایل. هیچ سطح پایین‌تری نمی‌تواند از آن بالاتر برود (`V021`) |
| `deny_users` | فهرست نام کاربری | این حساب‌ها هیچ‌جا نمی‌توانند push کنند |
| `deny_groups` | فهرست گروه GitLab | اعضای این گروه‌ها هیچ‌جا نمی‌توانند push کنند (نیاز به همگام‌سازی گروه‌ها دارد؛ قدم ۷) |
| `mode` | `enforce` (پیش‌فرض) یا `audit` | `audit` یعنی تخلف‌های mandatory فقط ثبت می‌شوند و push رد نمی‌شود. فقط برای شروع کار |

**چه چیزی در mandatory بگذاریم؟** فقط چیزهایی که **واقعاً هیچ استثنایی** برایشان قابل قبول نیست: فایل‌های اجرایی، سقف حجم کلی، حساب‌های مسدود. هر چیزی که ممکن است یک تیم دلیل موجهی برایش داشته باشد، در `defaults` برود.

> نکته: `mandatory` کلیدهای `refs` و `unblock_*` **ندارد**؛ کف امنیتی به شاخه وابسته نیست (`V022`).

---

## ۳. قدم ۳: پیش‌فرض سازمانی (`defaults`)

فایل: [`step3-defaults.yaml`](../../examples/tutorial/step3-defaults.yaml)

`defaults` قانون همه‌ی پروژه‌هاست، ولی **قابل تنظیم**: یک namespace یا پروژه می‌تواند به آن اضافه کند، سخت‌ترش کند، یا (با `unblock_*`) موردی را از آن بردارد.

<div dir="ltr">

```yaml
defaults:
  blocked_extensions: [pdb, nupkg, zip, 7z, msi]
  blocked_paths:
    - "**/bin/Debug/**"
    - "**/bin/Release/**"
    - "**/obj/**"
    - "**/packages/**"
  max_file_size: 20MiB
```

</div>

| کلید | معنی |
|---|---|
| `blocked_extensions`، `blocked_paths`، `blocked_signatures` | مثل mandatory، ولی قابل برداشتن در سطح پایین‌تر |
| `max_file_size` | سقف پیش‌فرض؛ باید ≤ سقف mandatory باشد |
| `refs` | قانون برای شاخه‌ها یا تگ‌های خاص (قدم ۵) |

- `**/obj/**` یعنی «هر فایلی داخل هر پوشه‌ای به نام `obj`، در هر عمقی». الگوها در بخش ۱۴ کامل توضیح داده شده‌اند.
- `defaults` کلید `unblock_*` ندارد (چیزی بالاتر از آن نیست که بخواهد برداشته شود).

---

## ۴. قدم ۴: قانون برای یک واحد یا یک پروژه (`namespaces`، `projects`)

فایل: [`step4-scopes.yaml`](../../examples/tutorial/step4-scopes.yaml)

<div dir="ltr">

```yaml
namespaces:
  finance:                            # group finance and ALL its subgroups/projects
    blocked_extensions: [pfx, p12]    # added on top of defaults
    max_file_size: 10MiB              # stricter than defaults
projects:
  finance/reporting:
    unblock_extensions: [zip]         # this project ships zipped templates
  data/models:
    max_file_size: 45MiB              # looser than defaults, still <= mandatory 50MiB
  sandbox/playground:
    enabled: false                    # mandatory rules only
```

</div>

- **`namespaces`**: کلید، مسیر یک گروه یا زیرگروه GitLab است (`finance` یا `finance/payments`). قانون روی **همه‌ی** پروژه‌ها و زیرگروه‌های زیر آن اعمال می‌شود. مقایسه بخش به بخش است: `finance` شامل `finance-old/app` **نمی‌شود**.
- **`projects`**: کلید، مسیر **کامل** پروژه است: `گروه/پروژه` یا `گروه/زیرگروه/پروژه`. یک بخش تنها (`finance`) خطای `V012` می‌دهد.
- **فهرست‌ها جمع می‌شوند:** پروژه‌ی `finance/app` هر چه در defaults هست **به‌علاوه‌ی** `pfx` و `p12` را منع می‌کند.
- **اندازه: دقیق‌ترین سطح برنده است**، چه سخت‌تر باشد چه آسان‌تر، ولی هرگز بالاتر از mandatory.
- **`unblock_extensions` / `unblock_paths` / `unblock_signatures`**: موردی را که سطح بالاتر (defaults یا namespace) منع کرده برمی‌دارد. چیزی را که در mandatory است **نمی‌توان** برداشت (`V020`). برداشتن چیزی که اصلاً منع نشده هشدار `W003` می‌دهد.
- **`enabled: false`** (فقط در `projects`): برای این پروژه فقط mandatory اجرا می‌شود و بقیه‌ی قوانین محتوایی خاموش است.
- **`mode`**: در هر namespace یا پروژه می‌توانید `mode: audit` بگذارید (قدم ۶).

با `explain` ببینید نتیجه برای یک پروژه چه می‌شود:

<div dir="ltr">

```
$ git-policy explain --policy step4-scopes.yaml --project finance/payment-api --user bob --groups developers
...
Effective content rules
  blocked extensions: 7z (defaults), dll (MANDATORY), exe (MANDATORY), msi (defaults), nupkg (defaults),
                      p12 (namespaces["finance"]), pdb (defaults), pfx (namespaces["finance"]), zip (defaults)
  blocked paths:      **/bin/debug/** (defaults), **/bin/release/** (defaults), **/obj/** (defaults), **/packages/** (defaults)
  blocked signatures: pe (MANDATORY)
  max file size:      10MiB (namespaces["finance"].max_file_size)
```

</div>

جلوی هر قانون نوشته شده از **کجای policy** آمده است.

---

## ۵. قدم ۵: قانون برای شاخه‌ها و تگ‌ها (`refs`)

فایل: [`step5-refs.yaml`](../../examples/tutorial/step5-refs.yaml)

`refs` داخل `defaults`، هر namespace و هر project می‌آید. کلید آن الگوی **کامل** ref است:

<div dir="ltr">

```yaml
defaults:
  # ...
  refs:
    "refs/tags/**":                   # every tag in every project
      max_file_size: 5MiB
namespaces:
  finance:
    # ...
    refs:
      "refs/heads/release/**":        # release branches of finance only
        blocked_extensions: [pdf]
```

</div>

- شاخه‌ها: `refs/heads/...`؛ تگ‌ها: `refs/tags/...`. الگوی کوتاه مثل `release/*` یا `main` **خطاست** (`V005`)؛ بنویسید `refs/heads/release/*` یا `refs/heads/main`.
- داخل هر ref این کلیدها مجازند: `blocked_*`، `unblock_*`، `max_file_size`، `mode`. (ref داخل ref مجاز نیست.)
- ref هر سطح **بعد از** همان سطح اعمال می‌شود. مثلاً در شاخه‌ی `release/2.0` پروژه‌ی `finance/app`، ترتیب این است: defaults → refs در defaults → finance → refs در finance → پروژه → refs در پروژه.
- اگر چند الگوی ref در **یک سطح** هم‌زمان مطابقت کنند، فهرست‌هایشان با هم جمع می‌شوند، **کوچک‌ترین** اندازه برنده است، و اگر یکی `enforce` باشد، `enforce` برنده است.

---

## ۶. قدم ۶: اجرای امن قانون جدید (`mode: audit`)

فایل: [`step6-audit.yaml`](../../examples/tutorial/step6-audit.yaml)

قانون جدید را اول در حالت **audit** بگذارید: push رد **نمی‌شود**، ولی هر تخلف با رویداد `WOULD_REJECT` در audit log ثبت می‌شود. بعد از یکی دو هفته، لاگ‌ها را بررسی کنید (Jenkins → `ACTION=EXPORT_LOGS`)، با تیم‌ها هماهنگ کنید و سپس `mode` را حذف کنید یا `enforce` بگذارید.

<div dir="ltr">

```yaml
namespaces:
  legacy:
    mode: audit                       # log only, for this namespace
```

</div>

| کجا | اثر |
|---|---|
| `settings.mode: audit` | همه‌ی قوانین **غیر** mandatory در کل سازمان فقط ثبت می‌شوند |
| `namespaces.X.mode` / `projects.X.mode` / داخل `refs` | فقط برای همان محدوده. دقیق‌ترین سطح برنده است؛ پس می‌توان کل سازمان را audit گذاشت و یک namespace را `enforce` کرد، یا برعکس |
| `mandatory.mode: audit` | فقط برای روز اول نصب؛ حتی DLL هم رد نمی‌شود. بعد از بررسی حتماً برش دارید |

> قوانین هویتی (`users`، `groups`، `deny_users`، `deny_groups`) و محدودیت‌های push تحت تأثیر `mode` نیستند؛ `mode` فقط برای قوانین **محتوایی** است (پسوند، مسیر، امضا، اندازه).

---

## ۷. قدم ۷: چه کسی کجا push کند (`users`، `groups`)

فایل: [`step7-identity.yaml`](../../examples/tutorial/step7-identity.yaml)

<div dir="ltr">

```yaml
mandatory:
  # ...
  deny_groups: [blocked-accounts]
users:
  alex:
    projects:
      finance/payment-api: {push: deny}     # alex may not push to this project
  intern.sara:
    push: deny                              # nowhere ...
    namespaces:
      training: {push: allow}               # ... except under training/
groups:
  contractors:
    push: deny                              # contractors: nowhere ...
    namespaces:
      outsourcing: {push: allow}            # ... except outsourcing/
  qa-team:
    refs:
      "refs/tags/**": deny                  # QA may not create or move tags
```

</div>

**ساختار هر کاربر یا گروه:**

| کلید | مقدار | معنی |
|---|---|---|
| `push` | `allow` / `deny` | قانون در سطح کل سرور |
| `refs` | `{"<الگوی ref>": allow/deny}` | قانون برای شاخه یا تگ خاص در کل سرور |
| `namespaces` | `{"<مسیر گروه>": {push: …, refs: {…}}}` | قانون در یک namespace |
| `projects` | `{"<مسیر پروژه>": {push: …, refs: {…}}}` | قانون در یک پروژه |

**قواعد تصمیم** (به ترتیب):

1. **mandatory** اول بررسی می‌شود: `deny_users` و `deny_groups` همه‌جا رد می‌کنند.
2. **دقیق‌ترین سطحی** که قانون دارد تصمیم می‌گیرد: پروژه+ref > پروژه > namespace عمیق‌تر(+ref) > namespace(+ref) > کل سرور+ref > کل سرور.
3. در همان سطح، قانون **کاربر** بر قانون **گروه** مقدم است.
4. اگر باز هم تساوی باشد (مثلاً دو گروه، یکی allow و یکی deny)، **deny** برنده است.
5. اگر **هیچ** قانونی مطابقت نکند، push مجاز است و فقط مجوزهای خود GitLab اعمال می‌شود.

> **`allow` دسترسی اضافه نمی‌دهد.** git-policy فقط می‌تواند محدود کند. `allow` فقط یک `deny` کلی‌تر را برای محدوده‌ی خاصی خنثی می‌کند؛ کاربری که در GitLab نقش Developer ندارد، با `allow` هم نمی‌تواند push کند.

نمونه‌ی واقعی `explain`:

<div dir="ltr">

```
$ git-policy explain --policy step9-full.yaml --user carol --groups contractors --project outsourcing/portal
Identity
  => namespace(depth 1)       group allow groups["contractors"].namespaces["outsourcing"].push
     global                   group deny  groups["contractors"].push
Verdict:     ALLOW

$ git-policy explain --policy step9-full.yaml --user alex --groups developers --project finance/payment-api
Identity
  => project                  user  deny  users["alex"].projects["finance/payment-api"].push
Verdict:     REJECT
  USER_PUSH_DENIED  (users["alex"].projects["finance/payment-api"].push)
```

</div>

### گروه‌ها به همگام‌سازی نیاز دارند

در لحظه‌ی push هیچ درخواستی به GitLab API فرستاده نمی‌شود. عضویت گروه‌ها از یک فایل محلی خوانده می‌شود که job ‌`git-policy-sync-membership` در Jenkins هر ۱۵ دقیقه به‌روز می‌کند. پس:

- نام گروه را **دقیقاً** مثل مسیر گروه در GitLab بنویسید (`platform/sre` برای زیرگروه).
- اگر policy قانون **deny گروهی** دارد (مثل `deny_groups` یا `push: deny` برای یک گروه) و فایل عضویت هنوز ساخته نشده باشد، **همه‌ی pushها رد می‌شوند** (`MEMBERSHIP_UNAVAILABLE`). برای جلوگیری از این اتفاق، سرور خودش `enable` و `apply` را در این حالت رد می‌کند (`V042`). راه درست: اول policy را اعمال کنید، بعد job همگام‌سازی را یک بار اجرا کنید، بعد موتور را روشن کنید.
- اگر فایل عضویت کهنه شود (بیشتر از `hard_max_age`، پیش‌فرض ۲۴ ساعت)، قوانین **allow** گروهی نادیده گرفته می‌شوند ولی **deny**ها باقی می‌مانند. داده‌ی کهنه فقط محدود می‌کند و هرگز دسترسی نمی‌دهد.

> نکته‌ی deploy key: وقتی با deploy key push می‌شود، GitLab نام **سازنده‌ی کلید** را به‌عنوان کاربر می‌فرستد. قانون آن کاربر روی push با کلیدش هم اعمال می‌شود.

---

## ۸. قدم ۸: استثنا (`exceptions`)

فایل: [`step8-exceptions.yaml`](../../examples/tutorial/step8-exceptions.yaml)

استثنا یعنی «این قانون، برای این افراد، در این محدوده، تا این تاریخ اجرا نشود». هر استثنا باید **محدود، مستند و تاریخ‌دار** باشد.

<div dir="ltr">

```yaml
exceptions:
  - id: EXC-2026-001                        # unique, UPPERCASE
    rules: [BLOCKED_EXTENSION]
    mandatory: true                         # needed because dll is a mandatory rule
    scope:
      projects: [finance/legacy-erp]
      refs: ["refs/heads/main"]
      paths: ["vendor/VendorSdk/*.dll"]     # only these files
    reason: Vendor SDK has no NuGet package yet; migration tracked in INFRA-2231.
    ticket: INFRA-2231
    approved_by: security-team
    expires: 2026-12-31

  - id: EXC-2026-002
    rules: [LIMIT_COMMITS, LIMIT_OBJECTS]
    subjects:
      users: [svc-migration]                # only this account
    scope:
      namespaces: [legacy]
    reason: One-time import of the old SVN history into GitLab.
    ticket: INFRA-2240
    expires: 2026-11-30

  - id: EXC-2026-003
    rules: [FILE_TOO_LARGE]
    mandatory: true                         # above the 50MiB mandatory cap
    subjects:
      groups: [data-science]
    scope:
      projects: [data/models]
    max_file_size: 200MiB
    reason: Model snapshots until the MLflow artifact store is ready.
    ticket: DS-118
    expires: 2027-01-31
```

</div>

| کلید | الزامی | توضیح |
|---|---|---|
| `id` | بله | شناسه‌ی یکتا: حروف **بزرگ** انگلیسی، عدد و `-`، ۳ تا ۶۴ کاراکتر، با حرف شروع شود. تکراری → `V030` |
| `rules` | بله | کد قانون‌هایی که استثنا می‌شوند (بخش ۱۵). `*` مجاز نیست؛ بعضی کدها قابل استثنا نیستند (`V035`) |
| `mandatory` | خیر (پیش‌فرض `false`) | برای عبور از قانونی که در `mandatory` است باید `true` باشد |
| `subjects.users` / `subjects.groups` | خیر | فقط برای این کاربران/گروه‌ها. اگر نباشد، برای همه |
| `scope.namespaces` / `scope.projects` | * | محدوده. **استثنای «همه، همه‌جا» ممنوع است:** یا `subjects` لازم است یا یک namespace/project (`V031`) |
| `scope.refs` | خیر | فقط این شاخه‌ها/تگ‌ها (الگوی کامل ref) |
| `scope.paths` | خیر | فقط این فایل‌ها (الگوی مسیر). فقط برای قوانین محتوایی؛ با قوانین هویتی یا محدودیت‌ها ترکیب نمی‌شود (`V033`) |
| `max_file_size` | خیر | فقط همراه `FILE_TOO_LARGE` (`V034`): سقف جدید اندازه در این محدوده. بالاتر از سقف mandatory فقط با `mandatory: true` (`W012`) |
| `reason` | بله | دلیل، حداقل ۱۰ کاراکتر |
| `ticket` | خیر | شماره‌ی درخواست، مثل `INFRA-2231` (حروف، عدد، `._#/-`، تا ۶۴ کاراکتر) |
| `approved_by` | خیر | چه کسی تأیید کرده |
| `expires` | بله | تاریخ `YYYY-MM-DD`؛ استثنا تا **پایان همان روز** معتبر است. حداکثر `settings.exceptions.max_lifetime_days` روز بعد از امروز (پیش‌فرض ۱۸۰، `V032`) |

- استثنای **منقضی‌شده** خطا نیست؛ فقط هشدار `W004` می‌دهد و دیگر اثری ندارد. آن را در تغییر بعدی حذف کنید.
- هر بار که استثنا مورد استفاده قرار می‌گیرد، با `id` آن در audit log ثبت می‌شود.

---

## ۹. قدم ۹: تنظیمات (`settings`) و پیام توسعه‌دهنده

فایل: [`step9-full.yaml`](../../examples/tutorial/step9-full.yaml) (policy کامل؛ بهترین نقطه‌ی شروع برای کپی)

<div dir="ltr">

```yaml
settings:
  mode: enforce                   # enforce | audit (for all non-mandatory rules)
  repository_types:               # all | mandatory_only | none
    project: all
    wiki: mandatory_only
    snippet: mandatory_only
    design: none
  limits:
    max_ref_updates: 1000         # refs per push
    max_new_commits: 50000        # new commits per push
    max_new_blobs: 500000         # new files (blobs) per push
    evaluation_timeout: 45s
  membership:
    soft_max_age: 2h              # older: warning in status
    hard_max_age: 24h             # older: group allows ignored, denies kept
    on_unavailable: deny_if_group_rules   # or ignore_groups
  audit:
    required: false               # true: reject pushes if the audit log cannot be written
    retention_days: 180
    log_accepted: true            # default false; true logs every accepted push too
  exceptions:
    max_lifetime_days: 180
  messages:
    header: "Push rejected by ACME Git policy."
    support: "Help: #devops-help or devops@acme.example"
    remediation:
      BLOCKED_EXTENSION: >-
        Binary dependencies do not belong in Git. Publish them as NuGet
        packages to Nexus (https://nexus.acme.example) and use PackageReference.
      BLOCKED_SIGNATURE: "This file is a Windows executable, whatever its name. Publish it to Nexus."
      FILE_TOO_LARGE: "Store large files in Nexus or Git LFS, not in Git."
      BLOCKED_PATH: "Build output (bin/, obj/, packages/) must not be committed. Check your .gitignore."
```

</div>

**کل بخش `settings` اختیاری است.** هر چیزی را که ننویسید مقدار پیش‌فرض دارد (جدول کامل در بخش ۱۲).

**پیامی که توسعه‌دهنده می‌بیند** از `messages` ساخته می‌شود:

<div dir="ltr">

```
remote: GL-HOOK-ERR: Push rejected by ACME Git policy.                  <- header
remote: GL-HOOK-ERR: User: dev
remote: GL-HOOK-ERR: Project: finance/app
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Rule: BLOCKED_EXTENSION
remote: GL-HOOK-ERR: Ref: refs/heads/main
remote: GL-HOOK-ERR: Commit: 5c1f0e2a...
remote: GL-HOOK-ERR: File: Lib/Mic.Caching.dll
remote: GL-HOOK-ERR: Blocked extension: .dll
remote: GL-HOOK-ERR:
remote: GL-HOOK-ERR: Required action (BLOCKED_EXTENSION): Binary dependencies do not belong in Git. ...   <- remediation
remote: GL-HOOK-ERR: Help: #devops-help or devops@acme.example         <- support
```

</div>

- برای هر کد قانون می‌توانید در `remediation` متن راهنمای خودتان را بنویسید (آدرس Nexus، لینک wiki، …). برای کدهایی که ننویسید، متن پیش‌فرض برنامه نمایش داده می‌شود.
- متن پیام **انگلیسی** بنویسید: خروجی git در ترمینال ویندوز فارسی را درست نشان نمی‌دهد.
- جزئیات داخلی (کدام خط policy باعث رد شد) در پیام **نیست**؛ فقط در audit log است.

---

## ۱۰. قدم ۱۰: تست قبل از merge

### ۱۰.۱ `validate`

<div dir="ltr">

```bash
git-policy validate test/policy.yaml
git-policy validate --now 2026-12-01 test/policy.yaml   # «در این تاریخ هم معتبر است؟» (انقضای استثناها)
```

</div>

کد خروج: `0` معتبر، `1` نامعتبر، `2` فایل پیدا نشد یا گزینه‌ی اشتباه. **ERROR** یعنی policy اعمال نمی‌شود؛ **WARNING** یعنی اعمال می‌شود ولی احتمالاً اشتباهی هست.

نمونه‌ی واقعی یک فایل پر از اشتباهات رایج:

<div dir="ltr">

```
  WARNING W001  mandatory.blocked_extensions[0]: leading dot in ".dll" is ignored
  ERROR   V005  defaults.max_file_size: invalid size "20MB" (use B, KiB, MiB or GiB, e.g. 20MiB)
  ERROR   V005  namespaces.release.refs["release/*"]: invalid ref pattern "release/*" (must be a full ref glob like refs/heads/release/*)
  ERROR   V012  projects.finance: project path must be <namespace>/<project>
  ERROR   V021  projects["finance/app"].max_file_size: 80MiB exceeds mandatory.max_file_size 50MiB
  ERROR   V020  projects["finance/app"].unblock_extensions: "dll" is blocked by mandatory and cannot be unblocked (use an exception with mandatory: true)
  ERROR   V005  exceptions[0].reason: a reason of at least 10 characters is required
  ERROR   V032  exceptions[0].expires: 2030-01-01 is more than 180 days away (settings.exceptions.max_lifetime_days)
RESULT: INVALID (7 errors, 1 warnings)
```

</div>

> اگر کلید ناشناخته (غلط املایی) باشد، مثلاً `blocked_path` به جای `blocked_paths`، فقط همان خطا (`V003`) نشان داده می‌شود و بقیه‌ی بررسی‌ها انجام نمی‌شوند. اول آن را درست کنید.

### ۱۰.۲ `explain`: «برای این آدم، در این پروژه، چه می‌شود؟»

<div dir="ltr">

```bash
git-policy explain --policy test/policy.yaml --user alex --groups developers --project finance/payment-api
git-policy explain --policy test/policy.yaml --user bob  --groups developers --project finance/app --ref refs/heads/release/2.0
git-policy explain --policy test/policy.yaml --user bob  --groups developers --project finance/app --ref refs/tags/v1.0
```

</div>

| گزینه | توضیح |
|---|---|
| `--policy FILE` | فایلی که می‌نویسید (بدون این گزینه، policy فعال سرور خوانده می‌شود) |
| `--project` | الزامی؛ مسیر کامل پروژه |
| `--user` | نام کاربری GitLab |
| `--groups a,b` | گروه‌های کاربر. **روی ماشین خودتان همیشه بدهید**؛ وگرنه اگر policy قانون گروهی داشته باشد، نتیجه `MEMBERSHIP_UNAVAILABLE` می‌شود چون فایل عضویت فقط روی سرور است |
| `--ref` | پیش‌فرض `refs/heads/main` |
| `--repository wiki-1` | برای دیدن رفتار روی wiki و … |
| `--json` | خروجی ماشینی |

همین کار در Jenkins: job `git-policy`، `ACTION=EXPLAIN` با `EXPLAIN_USER`، `EXPLAIN_PROJECT`، `EXPLAIN_REF`، `EXPLAIN_GROUPS`. آنجا policy **فعال** سرور و عضویت واقعی گروه‌ها استفاده می‌شود.

### ۱۰.۳ چک‌لیست قبل از MR

- [ ] `metadata.revision` یکی بالا رفته است.
- [ ] `validate` بدون ERROR است و WARNINGها را فهمیده‌اید.
- [ ] برای هر قانون جدید حداقل یک `explain` گرفته‌اید (یک مورد که باید رد شود، یک مورد که نباید).
- [ ] قانون جدید و پرریسک اول با `mode: audit` می‌رود.
- [ ] استثناها `reason`، `ticket` و `expires` معقول دارند و محدوده‌شان تا حد ممکن کوچک است.
- [ ] اگر قانون deny گروهی اضافه کرده‌اید، job همگام‌سازی گروه‌ها روی آن محیط کار می‌کند.
- [ ] تغییر `production/policy.yaml` معمولاً همان تغییری است که قبلاً روی `test/policy.yaml` امتحان شده است.

---

## ۱۱. قدم ۱۱: استقرار (GitOps)

<div dir="ltr">

```bash
git clone http://gitlab.example/platform/git-policy-config.git && cd git-policy-config
git switch -c block-pfx-in-finance
vi test/policy.yaml                     # change + metadata.revision: N+1
git-policy validate test/policy.yaml
git commit -am "finance: block pfx/p12 (INFRA-123)"
git push -u origin block-pfx-in-finance # then open a Merge Request
```

</div>

1. **قبل از merge:** در Jenkins، job `git-policy` را با `ACTION=VALIDATE_POLICY`، `ENVIRONMENT=TEST`، `POLICY_REF=block-pfx-in-finance` اجرا کنید. این بررسی نام کاربران، گروه‌ها و پروژه‌ها را با GitLab واقعی هم مقایسه می‌کند (هشدار `W010` برای نامی که در GitLab نیست).
2. **بعد از merge در `main`:** job `git-policy-gitops` حداکثر بعد از ۲ دقیقه `test/policy.yaml` را روی TEST اعمال و بررسی می‌کند. نتیجه را در همان job ببینید.
3. **PRODUCTION:** وقتی TEST درست کار کرد، همان تغییر را در `production/policy.yaml` بدهید (MR جدا یا همان MR). job gitops برای PRODUCTION فقط «drift» گزارش می‌دهد (build زرد). سپس job `git-policy` را با `ACTION=UPDATE_POLICY`، `ENVIRONMENT=PRODUCTION` و یک `REASON` اجرا کنید. Jenkins تفاوت با policy فعال را نشان می‌دهد و منتظر **تأیید نفر دوم** می‌ماند.
4. **برگرداندن:** یا در git revert کنید و revision را بالا ببرید (روش ترجیحی)، یا در Jenkins `ACTION=ROLLBACK_POLICY` بزنید (فوری، برای شرایط اضطراری).

---

## ۱۲. مرجع کامل کلیدها

### ۱۲.۱ سطح اول فایل

| کلید | نوع | الزامی |
|---|---|---|
| `apiVersion` | `git-policy/v1` | بله |
| `kind` | `GitPolicy` | بله |
| `metadata` | `name`، `revision`، `description` | بله |
| `settings` | بخش ۱۲.۲ | خیر |
| `mandatory` | بخش ۱۲.۳ | خیر |
| `defaults` | بخش ۱۲.۳ | خیر |
| `namespaces` | `{مسیر گروه: لایه}` | خیر |
| `projects` | `{مسیر پروژه: لایه}` | خیر |
| `users` | `{نام کاربری: قانون هویت}` | خیر |
| `groups` | `{مسیر گروه: قانون هویت}` | خیر |
| `exceptions` | فهرست استثنا | خیر |

### ۱۲.۲ `settings`

| کلید | مقدار مجاز | پیش‌فرض | معنی |
|---|---|---|---|
| `mode` | `enforce` / `audit` | `enforce` | حالت همه‌ی قوانین محتوایی غیر mandatory |
| `repository_types.project` | `all` / `mandatory_only` / `none` | `all` | repositoryهای معمولی |
| `repository_types.wiki` | همان | `mandatory_only` | wiki پروژه‌ها |
| `repository_types.snippet` | همان | `mandatory_only` | snippetها |
| `repository_types.design` | همان | `none` | Design Management |
| `limits.max_ref_updates` | ۱ تا ۱۰۰٬۰۰۰ | `1000` | حداکثر تعداد شاخه/تگ در یک push (`LIMIT_REF_UPDATES`) |
| `limits.max_new_commits` | ۱ تا ۱۰٬۰۰۰٬۰۰۰ | `50000` | حداکثر commit جدید در یک push (`LIMIT_COMMITS`) |
| `limits.max_new_blobs` | ۱ تا ۱۰۰٬۰۰۰٬۰۰۰ | `500000` | حداکثر فایل جدید در یک push (`LIMIT_OBJECTS`) |
| `limits.evaluation_timeout` | مدت، مثل `45s` یا `2m` | `45s` | اگر بررسی بیشتر طول بکشد، push رد می‌شود (`EVAL_TIMEOUT`) |
| `membership.soft_max_age` | مدت | `2h` | بعد از آن، status هشدار می‌دهد |
| `membership.hard_max_age` | مدت (≥ soft، `V041`) | `24h` | بعد از آن، allowهای گروهی نادیده گرفته می‌شوند |
| `membership.on_unavailable` | `deny_if_group_rules` / `ignore_groups` | `deny_if_group_rules` | اگر فایل عضویت اصلاً نباشد: رد همه‌ی pushها (اگر قانون گروهی هست) یا نادیده گرفتن قوانین گروهی |
| `audit.required` | `true` / `false` | `false` | `true`: اگر audit log قابل نوشتن نباشد، push رد شود (`AUDIT_UNAVAILABLE`) |
| `audit.retention_days` | ۱ تا ۳۶۵۰ | `180` | نگهداری audit log (برای `PRUNE_LOGS`) |
| `audit.log_accepted` | `true` / `false` | `false` | ثبت pushهای **پذیرفته‌شده** هم (برای SIEM) |
| `exceptions.max_lifetime_days` | ۱ تا ۳۶۶ | `180` | حداکثر فاصله‌ی `expires` از امروز |
| `messages.header` | متن | `Push rejected by organizational Git policy.` | خط اول پیام |
| `messages.support` | متن | — | خط آخر پیام (کانال پشتیبانی) |
| `messages.remediation` | `{کد قانون: متن}` | متن داخلی برنامه | راهنمای رفع مشکل برای هر کد |

- **`all`**: همه‌ی قوانین. **`mandatory_only`**: فقط mandatory (قوانین هویتی و لایه‌های دیگر اجرا نمی‌شوند). **`none`**: git-policy اصلاً بررسی نمی‌کند.
- واحد مدت: `s`، `m`، `h` (مثل `30s`، `45m`، `2h`). `d` قبول نیست.

### ۱۲.۳ لایه‌های محتوایی

| کلید | `mandatory` | `defaults` | `namespaces.X` / `projects.X` | داخل `refs` |
|---|:-:|:-:|:-:|:-:|
| `blocked_extensions` | ✓ | ✓ | ✓ | ✓ |
| `blocked_paths` | ✓ | ✓ | ✓ | ✓ |
| `blocked_signatures` (`pe`) | ✓ | ✓ | ✓ | ✓ |
| `max_file_size` | ✓ (سقف مطلق) | ✓ | ✓ | ✓ |
| `mode` | ✓ | — | ✓ | ✓ |
| `unblock_extensions` / `unblock_paths` / `unblock_signatures` | — | — | ✓ | ✓ |
| `refs` | — | ✓ | ✓ | — |
| `deny_users` / `deny_groups` | ✓ | — | — | — |
| `enabled` | — | — | فقط `projects` | — |

### ۱۲.۴ قانون هویت (`users.X` و `groups.X`)

| کلید | مقدار |
|---|---|
| `push` | `allow` / `deny` |
| `refs` | `{"refs/...": allow/deny}` |
| `namespaces` | `{"مسیر": {push: allow/deny, refs: {...}}}` |
| `projects` | `{"گروه/پروژه": {push: allow/deny, refs: {...}}}` |

### ۱۲.۵ استثنا

جدول کامل در قدم ۸.

---

## ۱۳. قواعد اولویت (خلاصه‌ی یک‌جا)

| موضوع | قاعده |
|---|---|
| **فهرست‌های منع** (پسوند، مسیر، امضا) | همه‌ی سطوح **جمع** می‌شوند. فقط `unblock_*` صریح در سطح پایین‌تر چیزی را برمی‌دارد، و هرگز چیزی از mandatory را |
| **در یک سطح، block و unblock یک مورد** | block برنده است |
| **اندازه‌ی فایل** | آخرین (دقیق‌ترین) سطحی که `max_file_size` دارد برنده است؛ ترتیب: defaults ← refs در defaults ← namespace بالا ← namespace پایین ← پروژه ← refs در پروژه. هرگز بالاتر از mandatory |
| **چند الگوی ref در یک سطح** | فهرست‌ها جمع، کوچک‌ترین اندازه، `enforce` بر `audit` |
| **`mode`** | دقیق‌ترین سطح برنده است؛ mandatory حالت جدای خودش را دارد |
| **هویت** | mandatory ← دقیق‌ترین سطح ← کاربر بر گروه ← deny بر allow ← بدون قانون = مجاز |
| **استثنا** | فقط قانون‌های نام‌برده، فقط در محدوده، فقط تا تاریخ؛ برای mandatory فقط با `mandatory: true` |
| **فایل عضویت کهنه** | فقط محدود می‌کند: deny گروهی می‌ماند، allow و استثنای گروهی نادیده |

---

## ۱۴. الگوها، نام‌ها و واحدها

### ۱۴.۱ الگوی مسیر و ref

| الگو | معنی | مثال مطابق | مثال نامطابق |
|---|---|---|---|
| `*` | هر چیزی **داخل یک** بخش مسیر | `*.dll` ← `a.dll` | `lib/a.dll` |
| `?` | دقیقاً یک کاراکتر | `v?.txt` ← `v1.txt` | `v10.txt` |
| `**` | صفر یا چند بخش (فقط به‌عنوان یک بخش کامل) | `**/*.dll` ← `a.dll`، `x/y/a.dll` | — |
| `dir/**` | هر چیزی **زیر** `dir` | `obj/**` ← `obj/a.o` | خود `obj` |

- الگو از **ریشه‌ی repository** شروع می‌شود: `obj/**` فقط `obj` در ریشه است؛ برای هر عمقی بنویسید `**/obj/**`.
- مسیرها **بدون حساسیت به بزرگی حروف** مقایسه می‌شوند (مثل ویندوز).
- `/` در ابتدا یا انتها، `//`، `.` و `..`، و `[ ] { } \` مجاز نیستند (`V023`).
- الگوی ref همان قواعد را دارد ولی باید با `refs/` شروع شود: `refs/heads/release/*`، `refs/tags/v*`، `refs/heads/**`.

### ۱۴.۲ پسوند

- بدون نقطه بنویسید: `dll` (با نقطه هشدار `W001` می‌گیرد و نقطه حذف می‌شود).
- پسوند چندبخشی مجاز است: `tar.gz`. فایل `a.tar.gz` هم با `gz` و هم با `tar.gz` مطابقت دارد.
- بزرگی حروف مهم نیست؛ `Setup.EXE` با `exe` رد می‌شود.

### ۱۴.۳ اندازه

- فقط واحدهای دودویی: `B`، `KiB`، `MiB`، `GiB`. مثل `512KiB`، `20MiB`، `1GiB`.
- `MB`، `GB`، `20 MiB` (با فاصله) و عدد اعشاری **خطا** هستند (`V005`).

### ۱۴.۴ نام‌ها

- **کاربر:** نام کاربری GitLab (مثل `alex`، `intern.sara`). بزرگی حروف مهم نیست؛ `Alex` و `alex` در یک فایل با هم خطای `V010` می‌دهند.
- **گروه / namespace:** مسیر کامل در GitLab، مثل `finance` یا `finance/payments` (نه نام نمایشی).
- **پروژه:** مسیر کامل، حداقل دو بخش: `finance/payment-api`.

---

## ۱۵. کدهای قانون (rule codes)

این کدها در پیام توسعه‌دهنده، در audit log، در `messages.remediation` و در `exceptions.rules` استفاده می‌شوند.

| کد | کِی | قابل استثنا؟ |
|---|---|:-:|
| `MANDATORY_USER_DENIED` | کاربر در `mandatory.deny_users` | ✓ (`mandatory: true`) |
| `MANDATORY_GROUP_DENIED` | کاربر عضو گروهی در `mandatory.deny_groups` | ✓ (`mandatory: true`) |
| `USER_PUSH_DENIED` | قانون `deny` کاربر | ✓ |
| `GROUP_PUSH_DENIED` | قانون `deny` گروه | ✓ |
| `BLOCKED_EXTENSION` | پسوند منع‌شده | ✓ |
| `BLOCKED_PATH` | مسیر منع‌شده | ✓ |
| `BLOCKED_SIGNATURE` | فایل اجرایی ویندوز (PE) | ✓ |
| `FILE_TOO_LARGE` | فایل بزرگ‌تر از سقف | ✓ |
| `LIMIT_REF_UPDATES` | تعداد ref بیش از حد | ✓ |
| `LIMIT_COMMITS` | تعداد commit بیش از حد | ✓ |
| `LIMIT_OBJECTS` | تعداد فایل بیش از حد | ✓ |
| `EVAL_TIMEOUT` | بررسی بیش از `evaluation_timeout` | ✗ |
| `POLICY_UNAVAILABLE` | policy روی سرور قابل خواندن نیست | ✗ |
| `MEMBERSHIP_UNAVAILABLE` | فایل عضویت نیست و policy قانون گروهی دارد | ✗ |
| `AUDIT_UNAVAILABLE` | `audit.required: true` و لاگ قابل نوشتن نیست | ✗ |
| `INVALID_REF_UPDATE` | ورودی نامعتبر از Git | ✗ |
| `INTERNAL_ERROR` | خطای داخلی موتور | ✗ |

---

## ۱۶. کدهای خطا و هشدار `validate`

### خطاها (policy اعمال نمی‌شود)

| کد | علت | راه رفع |
|---|---|---|
| `V001` | YAML خراب، فایل خالی، چند سند در یک فایل، یا استفاده از anchor/alias/`<<` | YAML را درست کنید؛ anchor مجاز نیست، هر چیز را صریح بنویسید |
| `V002` | کلید تکراری | یکی را حذف کنید |
| `V003` | کلید ناشناخته (غلط املایی یا کلید در جای اشتباه) | نام کلید را با بخش ۱۲ مقایسه کنید |
| `V004` | `apiVersion` یا `kind` اشتباه | `git-policy/v1` و `GitPolicy` |
| `V005` | مقدار نامعتبر: اندازه، مدت، تاریخ، نام، الگوی ref، مقدار خارج از بازه، … | متن خطا دقیقاً می‌گوید چه چیزی |
| `V010` | دو نام که فقط در بزرگی حروف فرق دارند | یکی کنید |
| `V011` | مسیر گروه/پروژه که در GitLab ممکن نیست (مثلاً ختم به `.git`) | مسیر واقعی را بنویسید |
| `V012` | پروژه با یک بخش | `گروه/پروژه` |
| `V020` | `unblock` چیزی که در mandatory است | از استثنا با `mandatory: true` استفاده کنید |
| `V021` | `max_file_size` بالاتر از سقف mandatory | کمتر کنید، یا استثنای `FILE_TOO_LARGE` با `mandatory: true` |
| `V022` | `refs` یا `unblock_*` داخل mandatory | mandatory آن‌ها را ندارد |
| `V023` | الگوی مسیر/ref نامعتبر | بخش ۱۴.۱ |
| `V030` | `id` تکراری در استثناها | شناسه‌ی یکتا |
| `V031` | استثنا برای همه در همه‌جا | `subjects` یا `scope.namespaces`/`scope.projects` اضافه کنید |
| `V032` | `expires` بیش از حد دور | تاریخ نزدیک‌تر (یا تمدید در آینده با تغییر جدید) |
| `V033` | `scope.paths` همراه قانون هویتی یا محدودیت | آن‌ها را در دو استثنای جدا بنویسید |
| `V034` | `max_file_size` در استثنا بدون `FILE_TOO_LARGE` | `rules: [FILE_TOO_LARGE]` |
| `V035` | `*` در `rules`، یا کد غیرقابل استثنا | کد دقیق از بخش ۱۵ |
| `V040` | revision بزرگ‌تر از policy فعال نیست (هنگام اعمال) | `metadata.revision` را بالا ببرید |
| `V041` | `soft_max_age` > `hard_max_age` | اصلاح کنید |
| `V042` | قانون deny گروهی ولی فایل عضویت روی سرور نیست (هنگام اعمال، وقتی موتور روشن است) | اول job همگام‌سازی گروه‌ها را اجرا کنید |

### هشدارها (policy اعمال می‌شود، ولی بررسی کنید)

| کد | علت |
|---|---|
| `W001` | پسوند با نقطه (`.dll`)؛ نقطه نادیده گرفته می‌شود |
| `W002` | مورد تکراری در یک فهرست |
| `W003` | `unblock` چیزی که هیچ سطح بالاتری منع نکرده |
| `W004` | استثنای منقضی‌شده (بی‌اثر است) |
| `W010` | کاربر، گروه یا پروژه در GitLab وجود ندارد (فقط در Jenkins `VALIDATE_POLICY`، یا با `--inventory`) |
| `W011` | قانون `allow` برای کاربر/گروهی که در mandatory مسدود است؛ هرگز اثر ندارد |
| `W012` | استثنای اندازه بالاتر از سقف mandatory بدون `mandatory: true` |

---

## ۱۷. دستور پخت: «می‌خواهم …»

<div dir="ltr">

```yaml
# ... DLL را همه‌جا منع کنم، حتی اگر اسمش را عوض کنند
mandatory:
  blocked_extensions: [dll]
  blocked_signatures: [pe]

# ... فایل‌های build (bin/obj) در هیچ پروژه‌ای commit نشود
defaults:
  blocked_paths: ["**/bin/Debug/**", "**/bin/Release/**", "**/obj/**"]

# ... یک پروژه اجازه‌ی zip داشته باشد (zip در defaults منع است)
projects:
  finance/reporting: {unblock_extensions: [zip]}

# ... در شاخه‌های release فایل بزرگ‌تر از 5MiB نیاید
defaults:
  refs:
    "refs/heads/release/**": {max_file_size: 5MiB}

# ... پیمانکاران فقط در namespace خودشان push کنند
groups:
  contractors:
    push: deny
    namespaces:
      outsourcing: {push: allow}

# ... فقط تیم release بتواند تگ بسازد
#     (همه‌ی کاربران گروه developers از تگ منع، release-team مجاز)
groups:
  developers:
    refs: {"refs/tags/**": deny}
  release-team:
    refs: {"refs/tags/**": allow}      # same level + tie -> deny wins for users in BOTH groups;
                                       # give release managers a user rule instead if needed

# ... حساب یک کارمند رفته را فوراً ببندم
mandatory:
  deny_users: [ali.old]

# ... یک قانون جدید را بدون ریسک امتحان کنم
namespaces:
  finance:
    mode: audit
    blocked_extensions: [pdf]

# ... یک پروژه‌ی قدیمی فقط کف امنیتی را داشته باشد
projects:
  legacy/old-app: {enabled: false}

# ... یک مهاجرت بزرگ یک‌باره از محدودیت تعداد commit عبور کند
exceptions:
  - id: EXC-MIGRATION-01
    rules: [LIMIT_COMMITS, LIMIT_OBJECTS]
    subjects: {users: [svc-migration]}
    scope: {projects: [legacy/erp]}
    reason: One-time import of the SVN history (INFRA-9).
    ticket: INFRA-9
    expires: 2026-10-31
```

</div>

---

## ۱۸. اشتباهات رایج

| اشتباه | نتیجه | درست |
|---|---|---|
| revision را بالا نبردن | Jenkins/سرور رد می‌کند (`V040`) | هر تغییر: `revision + 1` |
| `20MB` | `V005` | `20MiB` |
| `release/*` در refs | `V005` | `refs/heads/release/*` |
| `obj/**` برای «هر obj» | فقط ریشه را می‌گیرد | `**/obj/**` |
| `.dll` | هشدار `W001` | `dll` |
| `projects: {finance: …}` | `V012` | `namespaces: {finance: …}` |
| `unblock_extensions: [dll]` وقتی dll در mandatory است | `V020` | استثنا با `mandatory: true` |
| نام نمایشی گروه (`Finance Team`) | قانون هرگز مطابقت ندارد، W010 در Jenkins | مسیر گروه (`finance`) |
| deny گروهی بدون همگام‌سازی گروه‌ها | همه‌ی pushها رد می‌شوند؛ سرور از قبل جلوی آن را می‌گیرد (`V042`) | اول sync، بعد enable |
| انتظار اینکه `allow` دسترسی بدهد | اتفاق نمی‌افتد | دسترسی را در GitLab بدهید |
| قانون پرریسک مستقیم با enforce | pushهای تیم‌ها رد می‌شوند | اول `mode: audit` |
| ویرایش مستقیم روی سرور | با اجرای بعدی GitOps بازنویسی می‌شود، بدون سابقه | فقط از طریق repo و MR |

</div>
