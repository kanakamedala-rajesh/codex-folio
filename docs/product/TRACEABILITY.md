# VenkataSudha CodexFolio decision traceability

Status: implementation mapping for the completed Q1–Q122 grill

This map keeps the implementation from quietly losing settled product constraints. Detailed rationale remains in `DECISIONS.md` and `docs/adr/`; this file maps decision groups to delivery and acceptance evidence.

| Decision area | Grill questions | Primary milestone | Acceptance groups | Durable record |
|---|---|---|---|---|
| Companion boundary, CLI + dashboard, open-source MVP | Q1–Q3, Q16, Q48, Q115, Q118, Q122 | Phase 0 | PB, RG | `PRODUCT.md`, ADR 0001/0013/0032/0034 |
| Identity Profile model and homes | Q18–Q20, Q25, Q30, Q59, Q67, Q71, Q76–Q80 | M2 | PL | `CONTEXT.md`, ADR 0006/0021/0022 |
| Codex-owned authentication | Q8, Q10, Q28, Q61, Q64, Q99, Q102, Q105 | M2 | PL, SP | ADR 0002/0028/0030 |
| Transparent foreground launch and CLI contract | Q5, Q22, Q60, Q67, Q103–Q104, Q112–Q114 | M2 | PL, OP | ADR 0019/0021 |
| Usage metrics, provenance, scopes, totals | Q6, Q11–Q12, Q26–Q27, Q31, Q49–Q51, Q54–Q56, Q68, Q72–Q75 | M3 | UA | ADR 0011/0014/0017/0018 |
| Safe Continuation guarantee | Q4, Q9, Q23, Q35, Q37, Q44–Q45, Q57, Q62–Q63, cross-team clarification | M4 | CT | `../research/SAFE-CONTINUATION.md`, ADR 0003/0009/0011/0033 |
| Experimental Exact Continuation | Q23, Q65–Q67, Q95, Q114, Q118, Q121 | M6 | CT, RG | ADR 0003/0020/0034 |
| Shared Configuration Packs and Shared Work Home | Q30, Q34, Q36, Q68 | M2 and M6 | PL, CT, SP | `../research/SHARED-CONFIGURATION-PACK.md`, ADR 0006/0010 |
| Storage, encryption, retention, purge, backup | Q27, Q32, Q34, Q54, Q58, Q63, Q86, Q89, Q96, Q100 | M1–M4 | SP | `../research/CREDENTIAL-VAULT.md`, ADR 0008/0017/0025/0029/0033 |
| Loopback and single state owner | Q15, Q52–Q53, Q81–Q82, Q91–Q93 | M1 | SP, OP | `../architecture/ARCHITECTURE.md`, ADR 0005/0015/0016/0023/0027 |
| Collection scheduling and operational alerts | Q21, Q46, Q50, Q69, Q75, Q81 | M3 and M5 | UA, OP | ADR 0012/0014 |
| Telemetry and diagnostics | Q29, Q33, Q89–Q90, Q96–Q97, Q116 | M1 and M5 | SP, OP | ADR 0007/0012 |
| Dashboard hierarchy and data visualization | Q38–Q43, Q66, Q70, Q72–Q73, Q75, Q106–Q110 | M5 | UX | `../design/DASHBOARD-BRIEF.md`, ADR 0031 |
| Accessibility, localization, themes, responsive behavior | Q39–Q43, Q106–Q110 | M5 | UX | `../design/DASHBOARD-BRIEF.md`, ADR 0031 |
| Cross-platform distribution, signing, updates | Q7, Q14, Q83–Q85, Q88, Q120–Q121 | M7 | OP, RG | ADR 0024 |
| Offline frontend and generated API boundary | Q39, Q87, Q92 | M1 and M5 | SP, UX | `../architecture/ARCHITECTURE.md`, ADR 0004/0026/0027 |
| Architecture and production test standard | Q91–Q95, Q118–Q122 | All, especially M7 | RG | `../architecture/ARCHITECTURE.md`, ADR 0027/0034 |
| Portable config and new-device behavior | Q98–Q99, Q117 | M5 | SP, OP | ADR 0028/0033 |
| Onboarding and shell integration | Q101–Q105 | M2 | PL, OP | ADR 0019/0030 |

## Milestone evidence index

| Milestone | Required proof before exit |
|---|---|
| Phase 0 | reproducible clean-checkout checks and all target builds |
| M1 | fail-closed security, vault, migration, backup, and API-boundary tests |
| M2 | two-profile onboarding/switching/launch flows on every Tier 1 platform |
| M3 | source fixtures, provenance/freshness behavior, deduplication, retention, and safe export tests |
| M4 | reviewed repository-first handoff between profiles with no repo-local state |
| M5 | accepted route/state comps, WCAG evidence, responsive browser QA, explicit service lifecycle tests |
| M6 | failure-injected transactional rollback and automatic Safe fallback for every experiment |
| M7 | signed artifacts, clean-machine lifecycle, compatibility window, soak, and complete release documentation |

## Change control

If implementation evidence contradicts a settled assumption, do not silently weaken the requirement. Record the finding, update or add an ADR, identify the affected acceptance criteria, and request a product decision when the user-visible guarantee changes.
