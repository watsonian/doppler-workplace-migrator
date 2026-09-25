# Doppler Workplace Migrator — Styleguide

This styleguide defines the visual language for the migration tool, closely inspired by Doppler's brand identity. Since Doppler uses a proprietary font (DopplerRepro), we substitute Inter — a modern, clean sans-serif with similar characteristics that is freely available via Google Fonts.

---

## 1. Color System

### Brand Colors

```css
/* Primary — Doppler Purple */
--color-primary-50:  #F9F5FF;   /* Light background, hover fills */
--color-primary-100: #F0E6FF;   /* Selected/active backgrounds */
--color-primary-200: #E6D7FD;   /* Disabled state */
--color-primary-400: #A855F7;   /* Lighter accent */
--color-primary-500: #8B5CF6;   /* Mid-range accent */
--color-primary-600: #6B13F5;   /* PRIMARY BRAND — buttons, links, active states */
--color-primary-700: #41079B;   /* Hover state */
--color-primary-800: #2C056A;   /* Active/pressed state */
--color-primary-900: #1E0347;   /* Darkest purple */
```

### Neutral Colors

```css
/* Gray scale — cool-toned to complement purple */
--color-gray-50:  #F9FAFB;     /* Page background */
--color-gray-100: #F3F4F6;     /* Card backgrounds, subtle fills */
--color-gray-200: #E5E7EB;     /* Borders, dividers */
--color-gray-300: #D1D5DB;     /* Disabled text, placeholder */
--color-gray-400: #9CA3AF;     /* Muted text */
--color-gray-500: #6B7280;     /* Secondary text */
--color-gray-600: #4B5563;     /* Body text */
--color-gray-700: #374151;     /* Headings */
--color-gray-800: #1F2937;     /* Primary text */
--color-gray-900: #111827;     /* Darkest text */
```

### Semantic Colors

```css
/* Success — Green */
--color-success-50:  #ECFDF5;
--color-success-100: #D1FAE5;
--color-success-500: #10B981;
--color-success-600: #059669;
--color-success-700: #046822;

/* Warning — Amber */
--color-warning-50:  #FFFBEB;
--color-warning-100: #FEF3C7;
--color-warning-500: #F59E0B;
--color-warning-600: #D97706;

/* Error — Red */
--color-error-50:  #FEF2F2;
--color-error-100: #FEE2E2;
--color-error-500: #EF4444;
--color-error-600: #DC2626;
--color-error-700: #BF3F3F;

/* Info — Blue */
--color-info-50:  #EFF6FF;
--color-info-100: #DBEAFE;
--color-info-500: #3B82F6;
--color-info-600: #2563EB;
```

### Special Purpose

```css
--color-surface:     #FFFFFF;        /* Card and content surfaces */
--color-background:  #F9FAFB;       /* Page background (gray-50) */
--color-border:      #E5E7EB;       /* Default border (gray-200) */
--color-focus-ring:  rgba(107, 19, 245, 0.25); /* Focus ring — purple at 25% */
--color-overlay:     rgba(0, 0, 0, 0.5);       /* Modal overlays */
```

---

## 2. Typography

### Font Stack

```css
--font-sans: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
--font-mono: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', Consolas, monospace;
```

### Type Scale

| Name | Size | Weight | Line Height | Use |
|------|------|--------|-------------|-----|
| display | 30px / 1.875rem | 700 | 1.2 | Page titles (one per page) |
| heading-lg | 24px / 1.5rem | 600 | 1.3 | Section headings |
| heading-md | 20px / 1.25rem | 600 | 1.4 | Sub-section headings |
| heading-sm | 16px / 1rem | 600 | 1.5 | Card titles, form labels |
| body-lg | 16px / 1rem | 400 | 1.6 | Prominent body text, descriptions |
| body | 14px / 0.875rem | 400 | 1.5 | Default body text |
| body-sm | 13px / 0.8125rem | 400 | 1.5 | Secondary text, helper text |
| caption | 12px / 0.75rem | 500 | 1.4 | Labels, badges, metadata |
| mono | 13px / 0.8125rem | 400 | 1.5 | Code, technical values, tokens |

### Usage Rules

- **One display heading per page** — the wizard step title.
- **Headings never skip levels** — display → heading-lg → heading-md → heading-sm.
- **Body text defaults to `body` (14px)** — use `body-lg` only for intro paragraphs or descriptions that need emphasis.
- **Monospace for technical content** — token prefixes, API endpoints, environment slugs, config names, secret keys.

---

## 3. Spacing

### Scale (base: 4px)

```css
--space-0:  0;
--space-1:  4px;    /* 0.25rem */
--space-2:  8px;    /* 0.5rem  */
--space-3:  12px;   /* 0.75rem */
--space-4:  16px;   /* 1rem    */
--space-5:  20px;   /* 1.25rem */
--space-6:  24px;   /* 1.5rem  */
--space-8:  32px;   /* 2rem    */
--space-10: 40px;   /* 2.5rem  */
--space-12: 48px;   /* 3rem    */
--space-16: 64px;   /* 4rem    */
```

### Application Rules

| Context | Spacing |
|---------|---------|
| Page padding (horizontal) | space-6 to space-8 |
| Section gap (vertical) | space-8 to space-10 |
| Card padding | space-5 to space-6 |
| Form field gap | space-4 |
| Inline element gap | space-2 to space-3 |
| Icon-to-text gap | space-2 |
| Button internal padding | space-2 vertical, space-4 to space-5 horizontal |

---

## 4. Layout

### Wizard Container

```
┌──────────────────────────────────────────────────────────────────┐
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  HEADER: Logo + "Doppler Migration Tool"                  │  │
│  └────────────────────────────────────────────────────────────┘  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  STEPPER: Step 1 → Step 2 → Step 3 → ... → Step N        │  │
│  └────────────────────────────────────────────────────────────┘  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │                                                            │  │
│  │                    CONTENT AREA                            │  │
│  │              (max-width: 800px, centered)                  │  │
│  │                                                            │  │
│  │                                                            │  │
│  │                                                            │  │
│  └────────────────────────────────────────────────────────────┘  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  FOOTER: [Back]                              [Next/Start]  │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

- **Max content width**: 800px for form pages, 960px for plan/report pages.
- **Centered layout** with generous horizontal margins.
- **Sticky footer** with navigation buttons.
- **Stepper** always visible, showing current position.

### Responsive Behavior

- **Desktop (≥1024px)**: Full layout as above.
- **Tablet (768–1023px)**: Same layout, reduced margins.
- **Mobile (<768px)**: Stepper collapses to "Step N of M" text. Full-width content. This is a secondary use case — the tool is primarily used on desktop.

---

## 5. Components

### Buttons

**Primary Button** (main wizard actions: "Next", "Start Migration")
```css
background: var(--color-primary-600);     /* #6B13F5 */
color: white;
padding: 10px 24px;
border-radius: 7px;
font-weight: 500;
font-size: 14px;
transition: all 0.2s ease;
/* Hover: */ background: var(--color-primary-700);  /* #41079B */
/* Active: */ background: var(--color-primary-800); /* #2C056A */
/* Focus: */ box-shadow: 0 0 0 3px var(--color-focus-ring);
/* Disabled: */ background: var(--color-primary-200); cursor: not-allowed;
```

**Secondary Button** ("Back", "Cancel", secondary actions)
```css
background: white;
color: var(--color-gray-700);
border: 1px solid var(--color-gray-300);
padding: 10px 24px;
border-radius: 7px;
font-weight: 500;
/* Hover: */ background: var(--color-gray-50); border-color: var(--color-gray-400);
```

**Danger Button** (destructive actions, if any)
```css
background: var(--color-error-600);
color: white;
/* Hover: */ background: var(--color-error-700);
```

**Ghost Button** (tertiary actions)
```css
background: transparent;
color: var(--color-primary-600);
padding: 10px 16px;
/* Hover: */ background: var(--color-primary-50);
```

### Cards

**Default Card**
```css
background: white;
border: 1px solid var(--color-border);
border-radius: 8px;
padding: 20px 24px;
box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
```

**Interactive Card** (selectable items in plan view)
```css
/* Extends default card */
cursor: pointer;
transition: all 0.2s ease;
/* Hover: */
border-color: var(--color-primary-400);
box-shadow: 0 4px 12px rgba(107, 19, 245, 0.08);
transform: translateY(-1px);
```

**Status Card** (pre-flight check results, migration progress)
```css
/* Extends default card */
border-left: 3px solid <status-color>;
/* Success: */ border-left-color: var(--color-success-500);
/* Error: */   border-left-color: var(--color-error-500);
/* Warning: */ border-left-color: var(--color-warning-500);
/* Pending: */ border-left-color: var(--color-gray-300);
```

### Form Inputs

**Text Input**
```css
border: 1px solid var(--color-gray-300);
border-radius: 7px;
padding: 10px 14px;
font-size: 14px;
color: var(--color-gray-800);
background: white;
transition: border-color 0.15s ease;
/* Focus: */
border-color: var(--color-primary-600);
box-shadow: 0 0 0 3px var(--color-focus-ring);
outline: none;
/* Error: */
border-color: var(--color-error-500);
/* Placeholder: */
color: var(--color-gray-400);
```

**Token Input** (for Doppler API tokens)
```css
/* Extends text input */
font-family: var(--font-mono);
font-size: 13px;
letter-spacing: 0.02em;
/* Prefix badge showing token type (dp.ct., dp.pt., etc.) */
```

**Form Labels**
```css
font-size: 14px;
font-weight: 600;
color: var(--color-gray-700);
margin-bottom: 6px;
```

**Helper Text**
```css
font-size: 13px;
color: var(--color-gray-500);
margin-top: 4px;
```

### Badges / Tags

**Resource Type Badge** (used in plan tree and progress)
```css
display: inline-flex;
padding: 2px 8px;
border-radius: 4px;
font-size: 12px;
font-weight: 500;
/* Variants: */
/* Project: */  background: #EDE9FE; color: #6B13F5;
/* Env: */      background: #DBEAFE; color: #2563EB;
/* Config: */   background: #D1FAE5; color: #059669;
/* Secret: */   background: #FEF3C7; color: #D97706;
/* Role: */     background: #FCE7F3; color: #DB2777;
/* Group: */    background: #E0E7FF; color: #4338CA;
/* Manual: */   background: #FEE2E2; color: #DC2626;
```

**Status Badge**
```css
/* Same shape as above */
/* Completed: */ background: var(--color-success-100); color: var(--color-success-700);
/* Failed: */    background: var(--color-error-100);   color: var(--color-error-700);
/* Pending: */   background: var(--color-gray-100);    color: var(--color-gray-600);
/* Skipped: */   background: var(--color-warning-100); color: var(--color-warning-600);
/* Running: */   background: var(--color-info-100);    color: var(--color-info-600);
```

### Stepper

**Wizard Stepper** (horizontal step indicator)

```
  ●─────────●─────────●─────────○─────────○─────────○
Welcome   Config   Preflight   Plan    Migrate   Done
```

- **Completed step**: Solid purple circle with white checkmark, purple connector line.
- **Current step**: Solid purple circle (pulsing), gray connector ahead.
- **Future step**: Gray outline circle, gray connector.
- **Step labels**: caption size (12px), gray-500 for future, gray-800 for current, gray-600 for completed.

```css
/* Completed circle */
background: var(--color-primary-600);
border: 2px solid var(--color-primary-600);
color: white; /* checkmark */
width: 28px; height: 28px; border-radius: 50%;

/* Current circle */
background: var(--color-primary-600);
border: 2px solid var(--color-primary-600);
box-shadow: 0 0 0 4px var(--color-primary-100);
animation: pulse 2s ease-in-out infinite;

/* Future circle */
background: white;
border: 2px solid var(--color-gray-300);

/* Connector line */
height: 2px;
/* Completed: */ background: var(--color-primary-600);
/* Future: */    background: var(--color-gray-200);
```

### Progress Indicators

**Progress Bar** (data gathering, migration execution)
```css
/* Track */
background: var(--color-gray-200);
border-radius: 4px;
height: 8px;

/* Fill */
background: var(--color-primary-600);
border-radius: 4px;
transition: width 0.3s ease;
```

**Spinner** (loading states)
```css
border: 2px solid var(--color-gray-200);
border-top: 2px solid var(--color-primary-600);
border-radius: 50%;
width: 20px; height: 20px;
animation: spin 0.8s linear infinite;
```

**Rate Limit Indicator** (shown during gathering/migration)
```css
/* Small bar in the corner showing API budget */
/* Green when > 50% remaining */
/* Yellow when 10-50% remaining */
/* Red when < 10% or currently rate-limited */
```

### Alerts

**Info Alert**
```css
background: var(--color-info-50);
border: 1px solid var(--color-info-200, #BFDBFE);
border-radius: 8px;
padding: 14px 16px;
color: var(--color-info-600);
/* Icon: ℹ️ info circle in color-info-500 */
```

**Warning Alert**
```css
background: var(--color-warning-50);
border: 1px solid var(--color-warning-200, #FDE68A);
color: var(--color-warning-600);
```

**Error Alert**
```css
background: var(--color-error-50);
border: 1px solid var(--color-error-200, #FECACA);
color: var(--color-error-600);
```

**Success Alert**
```css
background: var(--color-success-50);
border: 1px solid var(--color-success-200, #A7F3D0);
color: var(--color-success-600);
```

### Tree View (Migration Plan Display)

Used to show the hierarchical migration plan:

```
▼ Workplace: My Company
  ├─ Settings (name, billing_email, security_email)
  ├─ ▼ Custom Workplace Roles (3)
  │    ├─ DevOps Lead
  │    ├─ Security Auditor
  │    └─ Viewer
  ├─ ▼ Custom Project Roles (2)
  │    ├─ Config Manager
  │    └─ Secret Reader
  ├─ ▼ Groups (2)
  │    ├─ Engineering (12 members)
  │    └─ DevOps (5 members)
  ├─ ▼ Projects (4)
  │    ├─ ▼ api-backend
  │    │    ├─ ▼ Environments (3)
  │    │    │    ├─ development
  │    │    │    ├─ staging
  │    │    │    └─ production
  │    │    ├─ ▼ Configs (5)
  │    │    │    ├─ dev (root) — 42 secrets
  │    │    │    ├─ dev_feature1 — 3 secrets
  │    │    │    ├─ stg (root) — 38 secrets
  │    │    │    ├─ prd (root) — 45 secrets
  │    │    │    └─ prd_hotfix — 2 secrets
  │    │    └─ Webhooks (1)
  │    └─ ▼ web-frontend
  │         └─ ...
  └─ ⚠ Manual Steps Required (3)
       ├─ 🔧 Integrations: 2 to configure
       ├─ 🔑 Service Tokens: 8 to recreate
       └─ 🔗 Secrets Syncs: check dashboard
```

**Tree styling:**
- Indent: 20px per level.
- Connector lines: `var(--color-gray-300)`, 1px.
- Expandable nodes: triangle toggle (▶/▼), clickable.
- Resource counts in `var(--color-gray-500)`.
- Secret counts in `var(--font-mono)`.
- Manual step items highlighted with warning color left border.
- Resource type badges inline next to names.

---

## 6. Page-Specific Design

### Welcome Page
- Large display heading: "Doppler Migration Tool"
- Subtitle in body-lg: brief description of what the tool does.
- Purple-accented illustration or icon area (can use a simple SVG graphic).
- Bulleted list of what will be migrated.
- Warning alert about prerequisites.
- Single "Get Started" primary button.

### Configuration Page
- Three form sections in cards:
  1. **Source (Doppler.com)**: Token input with validation feedback.
  2. **Target (On-Prem)**: Host URL input + token input.
  3. **Options**: Dev mode toggle with explanation.
- Inline validation (green check / red X) as user types.
- Token type auto-detection from prefix (show badge: "CLI Token", "Personal Token", etc.).

### Pre-flight Page
- Vertical checklist with animated status:
  - Gray circle → spinning → green check / red X.
  - Each check has a label and optional detail message.
- "All checks passed" or "N issues found" summary at bottom.
- Errors are expandable to show details.

### Data Gathering Page
- Per-resource progress sections.
- Overall progress bar at top.
- Rate limit indicator in corner.
- Resource count tallies updating in real-time.

### Plan Review Page
- Full tree view (see Tree View component above).
- Summary statistics card at top: "X projects, Y environments, Z configs, N secrets".
- Manual steps highlighted in a separate warning card.
- "Approve and Migrate" primary button.

### Migration Page
- Phase-by-phase progress.
- Current item indicator with spinner.
- Per-item status (check, X, skip icon).
- Error items expandable for details.
- Pause/Resume capability.
- Checkpoint status indicator.

### Service Token Report Page
- Table layout:
  | Project | Config | Token Name | Created | Expires |
  - All columns sortable.
- Explanation card above the table.
- "What to do" instructions in an info alert.

### Completion Page
- Large success icon (green checkmark in circle).
- Migration summary statistics.
- List of any failed items with details.
- List of remaining manual steps.
- "Download Report" button (JSON export of checkpoint + summary).

---

## 7. Iconography

Use **Lucide React** icons (open source, consistent style, tree-shakeable):

| Usage | Icon | Notes |
|-------|------|-------|
| Success / Completed | `CheckCircle2` | Green |
| Error / Failed | `XCircle` | Red |
| Warning | `AlertTriangle` | Amber |
| Info | `Info` | Blue |
| Pending | `Circle` | Gray outline |
| In Progress | `Loader2` | Purple, animated spin |
| Project | `FolderOpen` | |
| Environment | `Layers` | |
| Config | `FileCode` | |
| Secret | `KeyRound` | |
| Role | `Shield` | |
| Group | `Users` | |
| Webhook | `Webhook` | |
| Integration | `Plug` | |
| Service Token | `Key` | |
| Service Account | `Bot` | |
| Manual Step | `Wrench` | |
| Expand | `ChevronDown` | |
| Collapse | `ChevronRight` | |
| Back | `ArrowLeft` | |
| Next | `ArrowRight` | |
| Download | `Download` | |
| Rate Limit | `Gauge` | |

---

## 8. Animation & Motion

- **Transitions**: `all 0.2s ease` for interactive elements (buttons, cards).
- **Stepper pulse**: `2s ease-in-out infinite` on current step.
- **Spinner**: `0.8s linear infinite` rotation.
- **Progress bar fill**: `width 0.3s ease` for smooth growth.
- **Card hover lift**: `transform 0.2s ease` for `translateY(-1px)`.
- **Check appear**: `0.3s ease` scale from 0 to 1 when a check completes.
- **No gratuitous animation** — motion serves function (indicating progress, state change, interactivity).

---

## 9. Shadows

```css
--shadow-xs:  0 1px 2px rgba(0, 0, 0, 0.04);                          /* Subtle depth */
--shadow-sm:  0 1px 3px rgba(0, 0, 0, 0.04);                          /* Cards at rest */
--shadow-md:  0 4px 12px rgba(0, 0, 0, 0.06);                         /* Elevated cards */
--shadow-lg:  0 8px 24px rgba(0, 0, 0, 0.08);                         /* Modals, dropdowns */
--shadow-purple: 0 4px 12px rgba(107, 19, 245, 0.08);                 /* Purple-tinted hover */
```

---

## 10. Accessibility

- **Color contrast**: All text meets WCAG AA (4.5:1 for body, 3:1 for large text).
- **Focus indicators**: Visible focus ring (`3px var(--color-focus-ring)`) on all interactive elements.
- **Keyboard navigation**: All wizard steps navigable via Tab/Shift+Tab. Enter activates buttons.
- **Screen readers**: ARIA labels on all icon-only buttons, live regions for progress updates, proper heading hierarchy.
- **Reduced motion**: Respect `prefers-reduced-motion` — disable pulse, reduce transitions to instant.
