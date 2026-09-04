# Personal Home Design QA

## Comparison Target

- Source visual truth: `E:/code/msp-go/.tmp/personal-home-concept-1.png` (1487 x 1058).
- User-reported desktop crop: `C:/Users/Administrator/AppData/Local/Temp/codex-clipboard-a0d57afe-a195-4e30-aac1-80c952fd0c4e.png`.
- Student desktop implementation: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-student-final.png`.
- Teacher desktop implementation: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-teacher-final.png`.
- Student dark implementation: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-student-dark-final.png`.
- Student mobile implementation: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-student-mobile-final.png`.
- Mobile navigation state: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-mobile-menu-final.png`.
- Crop-fix wide desktop: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-hero-crop-fixed-wide.png`.
- Crop-fix 1440 desktop: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-hero-crop-fixed-1440.png`.
- Crop-fix dark desktop: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-hero-crop-fixed-dark-1440.png`.
- Implementation URL: `http://127.0.0.1:5173/home` after student or teacher login.
- Desktop state: authenticated student and teacher fixtures, light theme, 1440 x 1024 browser viewport. The Browser viewport could not reproduce the source's exact 1487 x 1058 inner viewport, so the 1432 x 1018 screenshot was proportionally normalized for comparison.
- Mobile state: authenticated student fixture, light theme, 390 x 844 browser viewport.

## Full-View Comparison Evidence

- Combined comparison: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-comparison-final.png`.
- The source and implementation were placed in one normalized side-by-side image before judgment; the comparison was also inspected with `view_image` alongside the source and latest screenshot.
- First-viewport balance is preserved: greeting and CTA on the left, calculus surface and notebook visual on the right, four-metric strip below, and a two-column continuation area.
- The implementation keeps the existing product header, role badge, navigation density, theme control, and icon family instead of introducing a second app shell.
- Student and teacher pages share one visual system while the data, copy, CTA, actions, recent activity, and affiliation adapt to role.

## Focused Region Comparison Evidence

- Hero and metrics: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-comparison-hero-final.png`.
- Content sections: `C:/Users/Administrator/.codex/visualizations/2026/07/21/019f86ea-94de-7ac3-a546-0601a2ff12bd/personal-home-comparison-sections-final.png`.
- The focused hero pass verified the H1, supporting sentence, CTA label/icon, continuation copy, image crop, metric labels, metric icons, and vertical spacing.
- The focused content pass verified headings, list density, progress treatment, timestamps, card radii, borders, icon alignment, and affiliation action placement.

## Required Fidelity Surfaces

- Fonts and typography: the project sans-serif stack is retained; the H1, section headings, metrics, labels, metadata, and controls have explicit sizes and weights with zero letter spacing. Long usernames and API-derived hero context use `overflow-wrap:anywhere`.
- Spacing and layout rhythm: the max-width, hero split, four-column metric strip, two-column content grid, 8 px radii, single-level cards, and restrained shadows preserve the source hierarchy without nested cards.
- Colors and visual tokens: the source's white, blue, violet, emerald, and coral balance maps to existing project tokens. The primary CTA now uses `primary-700` so white text passes AA in both themes.
- Image quality and asset fidelity: dedicated 69 KB light and 81 KB dark WebP assets preserve the calculus surface, notebook, pen, subject scale, and background integration. Both loaded at their natural dimensions with no placeholder, CSS drawing, inline SVG illustration, or broken crop.
- Copy and content: the source H1, student supporting sentence, CTA, and continuation context match. `累计学习` intentionally replaces the source's `本周学习` because the real overview API returns cumulative study time.
- Icons: all interface icons use the existing Lucide family with consistent outline weight, optical size, color, and alignment.
- States and interactions: student, teacher, light, dark, loading, partial failure, empty data, retry, theme toggle, mobile navigation open/close, and role-specific destinations are implemented.
- Accessibility: semantic headings and regions, labelled navigation, focus rings, status live region, reduced-motion handling, sufficient text contrast, mobile menu `aria-expanded`, and Escape dismissal are present.

## Above-The-Fold Copy Diff

- Matched: `早上好，林同学`, `坚持每天进步一点点，数学思维终将成就你的无限可能。`, `继续学习`, and the last-learning context.
- Intentional product additions: the existing role badge and authenticated user greeting remain in the header.
- Intentional data correction: `累计学习` reflects the backend contract; using `本周学习` would misrepresent cumulative minutes.
- Teacher copy is role-specific because the selected source only depicted the student state.

## Findings

No actionable P0, P1, or P2 findings remain.

## Intentional Deviations

- The source's scheduled times were not implemented because the product has no schedule contract. The same area uses real mastery data and common learning actions, avoiding fabricated times while preserving the two-column hierarchy and three-item rhythm.
- Existing project navigation includes `我的班级` and a role badge that were not shown in the concept. These are required product controls and remain visually consistent.
- The generated hero asset omits the source's freestanding formula annotation but preserves the mathematical surface, notebook, pen, palette, crop, and visual weight.

## Patches Made During QA

- Routed student and teacher login, root, logo, and role fallback paths to `/home`; administrators remain on `/admin/dashboard`.
- Added independent `Promise.allSettled` data regions so request failures do not become false zero or empty states.
- Corrected cumulative study-time labeling and converted student mastery ratios from `0..1` to `0..100` display percentages.
- Corrected teacher class-request failure copy and added an unexpected-payload fallback that exits loading and supports retry.
- Added light/dark calculus assets, role-aware sections, entrance/progress/float motion, and reduced-motion support for Framer Motion and skeleton pulses.
- Replaced the desktop hero image's `object-cover` plus 1.3x zoom with a 340 px media frame and right-aligned `object-contain` at 1.05x, keeping the calculus peaks visible without weakening the notebook and pen composition.
- Raised CTA and small-text contrast, added the partial-failure live region, and protected long dynamic hero text from clipping.
- Added a semantic mobile navigation landmark and a working overflow menu with Escape dismissal.
- Removed temporary preview and temporary test sources before handoff.

## Verification

- Browser/IAB desktop: student light, teacher light, and student dark rendered meaningful content with loaded assets, no horizontal overflow, and no new console warning/error entries after a clean preview reload.
- Browser responsive check: 390 x 844 mobile had no document-level horizontal overflow; the hero, image, metric cards, and text stayed within the viewport. The calculus image intentionally crops inside its overflow-hidden media frame.
- Desktop crop follow-up: the 2180 x 900 and 1440 x 1024 browser checks rendered a 672 x 340 media frame with `object-fit: contain`, `object-position: 100% 50%`, and `scale: 1.05`; every calculus peak remained inside the frame and the light asset loaded at its natural 1694 px width.
- Dark-theme crop follow-up: the dark asset loaded at its natural 1774 px width in the same 672 x 340 frame, with the peaks, notebook, and pen all visible.
- Mobile regression follow-up: the implementation diff is limited to `lg:` classes, so the base `object-cover`, `scale-[1.15]`, and `sm:scale-125` behavior shown in the existing real 390 x 844 mobile screenshot is unchanged. A current 382 px-wide DOM geometry check also reported `object-fit: cover`, `scale: 1.15`, and a 374 px document inside the 382 px viewport. Its browser screenshot output was truncated to 382 x 272 and is intentionally excluded from visual evidence.
- Console follow-up: the two existing data-request warnings were present before the style change and did not increase after reload, viewport changes, or theme interaction; no framework overlay or hero-related warning/error appeared.
- Interaction proof: theme toggled light/dark; mobile navigation changed `aria-expanded` from `false` to `true`, exposed all role links, and returned to `false` on Escape.
- Route protection: unauthenticated `/home` redirected to `/welcome`.
- Temporary contract tests: 2/2 passed for student mastery `0.72 -> 72%`, cumulative labeling, and teacher class-request failure semantics; the temporary test file was deleted.
- Earlier production-data helper verification: 18/18 temporary tests passed with 98.57% statements, 83.72% branches, 100% functions, and 100% lines for `homeData.ts`; those temporary sources were deleted as required.
- `npm.cmd run lint`: passed.
- `npm.cmd run build`: passed; only pre-existing Browserslist age and large-chunk advisory warnings remain.
- `git diff --check`: passed with line-ending notices only.
- Repository scan: no test/spec source, temporary preview entry, preview token, or test-only fixture remains.

## Residual Risk

- Safari and Firefox were not separately exercised.
- Real backend outage transitions were isolated in temporary tests and code review rather than induced against a live production account.
- Source and implementation viewport sizes differ slightly because the in-app browser's inner viewport could not be set to the source's exact dimensions; the comparison uses proportional normalization and an additional exact 390 x 844 responsive check.

final result: passed

---

# Embedding Configuration Design QA

## Comparison Target

- Source visual truth: supplied reference image `codex-clipboard-0697e733-113a-467c-8dd5-aaf4cb67f8bf.png`.
- Desktop active state: local QA artifact `output/playwright/embedding-config-qa/desktop-panel-active-collapsed.png` (not committed).
- Desktop successful-probe state: local QA artifact `output/playwright/embedding-config-qa/desktop-panel-success.png` (not committed).
- Mobile successful-probe state: local QA artifact `output/playwright/embedding-config-qa/mobile-panel-success.png` (not committed).
- Mobile dirty-contract state: local QA artifact `output/playwright/embedding-config-qa/mobile-panel-dirty.png` (not committed).
- Implementation URL: `http://localhost:5173/admin/ai-models`, under the `向量模型` tab.

## Comparison Evidence

- Combined reference and implementation: local QA artifact `output/playwright/embedding-config-qa/reference-vs-implementation.png` (not committed).
- The supplied reference and the current active-contract panel were normalized into one comparison image before judgment.
- The revised common path removes the blocking error banner and manual Revision/dimension fields. It keeps the existing title, model selector, status, probe, activation, and history hierarchy while moving specialist fields behind `高级设置`.

## Required Fidelity Surfaces

- Typography, neutral surfaces, cyan primary action, green active/success treatment, border weight, and Lucide icons continue to use the existing admin design system.
- The common path is one responsive row on desktop and a stable vertical flow on mobile. Long provider/model labels remain contained; the history table scrolls inside its own boundary.
- Revision is treated as an internal immutable identifier: the form does not ask administrators to invent it, and generated values appear as `系统生成` in history with the full value available as the cell title.
- A successful probe exposes the observed dimension and latency, enables activation, and any subsequent form change clears the probe, restores `待检测`, and disables activation.
- The dimensions control is conditional: it appears only when the administrator explicitly asks the upstream provider for a chosen output dimension.

## Findings

No actionable P0, P1, or P2 visual or interaction findings remain.

## Intentional Deviations

- The source exposed every protocol/runtime field at once. The implementation separates the model-first workflow from advanced vector-contract and runtime settings to avoid requiring provider-internal knowledge for normal setup.
- The raw generated revision is not repeated in the success banner because it is not an administrator input; immutable history retains it.
- The implementation includes the existing active-version history below the configuration surface, which was outside the supplied crop but is required for operational traceability.

## Verification

- Desktop Chromium at 1280 x 720: active restore, model switch, successful probe, advanced toggle, dirty-state invalidation, disabled/enabled activation, and history display passed.
- Mobile Chromium at 390 x 844: the panel, long model name, buttons, controls, success state, and advanced sections stayed within the viewport; the document measured 390 px client width and 374 px scroll width.
- Desktop measured 1280 px client width and 1264 px scroll width. Neither viewport had document-level horizontal overflow.
- Both browser sessions reported zero console errors and zero console warnings.
- The browser used fictitious provider/model fixtures and mocked admin API responses. Real provider compatibility was validated separately through the backend service without exposing credentials.

## Residual Risk

- Safari and Firefox were not separately exercised.
- Qdrant was unavailable during this pass, so this QA covers embedding configuration only and does not claim P2-B ingestion or P3 retrieval behavior.

final result: passed

---

# AI Channel Editor Design QA

## Comparison Target

- Source visual truth: `C:/Users/Administrator/AppData/Local/Temp/codex-clipboard-3c66d366-71c5-45e9-9060-52b519e3b02d.png`, `C:/Users/Administrator/AppData/Local/Temp/codex-clipboard-f2a5d23d-c859-4a11-9610-c919f7d17837.png`, and `C:/Users/Administrator/AppData/Local/Temp/codex-clipboard-491549e2-9c11-4d56-b905-7f6063d72c2f.png`.
- Desktop implementation: `C:/Users/Administrator/AppData/Local/Temp/msp-channel-desktop-final.png`, captured at 1568 x 1454 in the create-channel, OpenAI (Completion), light-theme state.
- Mobile implementation: `C:/Users/Administrator/AppData/Local/Temp/msp-channel-mobile-final.png`, captured at 390 x 844 in the same create-channel state.
- Mobile multi-key state: `C:/Users/Administrator/AppData/Local/Temp/msp-channel-mobile-multi.png`, captured at 390 x 844 after switching to multi-key mode.
- Default viewport model-section state: `C:/Users/Administrator/AppData/Local/Temp/msp-channel-model-section.png`, captured at 1280 x 720 with the model section and fixed footer visible.
- Implementation URL: `http://127.0.0.1:5173/admin/ai-models`; normal unauthenticated navigation redirects to the administrator login page.

## Full-View Comparison Evidence

- Combined desktop comparison: `C:/Users/Administrator/AppData/Local/Temp/msp-channel-desktop-comparison-final.png`.
- The reference and implementation were placed in one side-by-side image before judgment. The comparison covers the drawer frame, fixed header and footer, left step navigation, main form grid, section sequence, credential card, model area, typography hierarchy, and overall density.
- The implementation preserves the New API composition while fitting the existing product shell: a nearly full-width right drawer, restrained 8 px-or-less surfaces, a 306 px desktop step rail, and a 1440 px constrained content area.

## Focused Region Comparison Evidence

- Credential-region comparison: `C:/Users/Administrator/AppData/Local/Temp/msp-channel-credentials-comparison-final.png`.
- The focused pass verifies the credential section icon and heading, API address label/help copy, mode selector, key input proportions, borders, spacing, and the transition into the model section at readable scale.
- Additional implementation states were inspected in the desktop, mobile, mobile multi-key, and default-viewport captures because the source images do not show responsive collapse, key strategy controls, or keyboard states.

## Required Fidelity Surfaces

- Fonts and typography: the implementation intentionally retains the project's established sans-serif stack, weights, line heights, and zero letter spacing. The reference uses larger and denser UI type; matching it exactly would create a second typography system inside the admin product. Labels, helper text, placeholders, buttons, and long model/provider values wrap or truncate without overlap.
- Spacing and layout rhythm: the drawer width, fixed regions, step rail, two-column basic-information grid, full-width section dividers, compact cards, and vertical rhythm match the source hierarchy. The desktop width was increased to `calc(100vw - 3rem)` with a 1600 px maximum, and the content area was widened to 1440 px during QA.
- Colors and visual tokens: neutral surfaces, subtle borders, cyan primary controls, green credentials, and magenta model accents map the reference intent to existing project tokens. Active, focus, disabled, and destructive states retain sufficient visual distinction without introducing gradients or decorative effects.
- Image quality and asset fidelity: the target is a form-based operational interface and contains no photographic or illustrative assets. Provider and section marks use the project's installed icon family; no placeholder imagery, CSS drawing, emoji, custom inline SVG illustration, or generated raster asset is used.
- Copy and content: visible labels follow the reference where supported, including create/edit titles, paste connection information, basic information, credentials, models and groups, and advanced settings. The OpenAI Organization field shown in one reference is intentionally omitted because the current backend contract has no organization identifier or header support.
- Icons: provider, section, action, close, copy, fetch, and save controls use a consistent library and stroke weight. Unfamiliar icon-only controls expose a tooltip or title; the mobile paste button also has an accessible name.
- States and interactions: single-key, batch-create, and multi-key modes work; duplicate keys are removed; round-robin and random strategies switch; connection information can be pasted; models can be preset, fetched, copied, cleared, and mapped visually or as JSON; Escape closes the drawer; Tab and Shift+Tab remain trapped inside it; partial batch failures retain only the keys that still need retrying.
- Responsiveness: the desktop step rail becomes a compact mobile flow, fields collapse to one column, the fixed footer remains reachable, and the 390 x 844 captures show no document-level horizontal scrollbar or incoherent overlap. The 1280 x 720 state keeps the primary actions visible while the form body scrolls independently.
- Accessibility: semantic labels, required-field markers, visible focus treatment, labelled icon controls, Escape dismissal, keyboard focus containment, stable tap targets, and non-overlapping wrapped text are present. Reduced viewport width does not hide the close or primary action controls.

## Findings

No actionable P0, P1, or P2 findings remain.

## Intentional Deviations

- The implementation keeps the existing admin typography and control density instead of copying the reference's larger type one-for-one; the information hierarchy and scan order remain equivalent.
- The OpenAI Organization field is not rendered because there is no corresponding backend persistence or outbound-header contract. Adding a visual-only field would imply unsupported behavior.
- Product-specific provider icons and labels follow the current provider catalog rather than hard-coding every option visible in New API.

## Patches Made During QA

- Expanded the desktop drawer and content proportions so the step navigation no longer consumes excessive horizontal space relative to the form.
- Removed mobile document-level horizontal overflow with an explicit `overflow-x-hidden` boundary.
- Added `aria-label` and `title` to the compact mobile paste-connection button.
- Verified and retained fixed header/footer behavior, keyboard focus trapping, and responsive section navigation after the layout changes.

## Verification

- Desktop browser check: the 1568 x 1454 create-channel state rendered meaningful content, the fixed header/footer and four-step rail remained stable, and the clean preview produced no new console error or warning.
- Default viewport check: at 1280 x 720, the model section could be reached while the footer actions remained visible.
- Mobile browser check: at 390 x 844, the drawer, fields, section headings, footer actions, and multi-key controls stayed within the viewport with no visible horizontal scrollbar.
- Keyboard check: Escape closed and reopening restored the drawer; Tab from the final action wrapped to the first drawer control, and Shift+Tab from the first control wrapped to the submit action.
- Credential behavior check: duplicate keys were removed, repeated `api_key` connection lines were retained before de-duplication, and round-robin/random strategy switching updated the form state.
- Temporary directed tests passed for duplicate connection keys and malformed keyring handling; all temporary test sources and fixtures were removed afterward.
- `npm.cmd test -- --passWithNoTests --reporter=dot`, `npm.cmd run lint`, `npm.cmd run build`, `go test ./... -count=1`, and `go vet ./...` passed. The frontend build only reported the existing Browserslist age and large-chunk advisories.

## Residual Risk

- Safari and Firefox were not separately exercised.
- Real external providers were not used to validate rate limits, cost distribution, or response quality across several keys.
- A future OpenAI Organization feature requires an explicit backend contract before the reference-only field can be added honestly.

final result: passed
