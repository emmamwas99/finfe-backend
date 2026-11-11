# Financial health formulas

The health engine returns explained checks and actionable advice, never a single opaque score. It evaluates the last three calendar months in one currency, using only months with recorded entries. The API obtains transaction aggregates in SQL and passes integer minor units to the pure engine.

- Cash flow = earned income minus expenses. Borrowed income is shown separately and does not inflate earned income.
- Living within income = spending divided by planned income. Good at at most 90%, warning through 100%, bad above 100%.
- Borrowing = new borrowed money divided by planned income. Zero is good, at most 10% warns, above 10% is bad.
- Savings and investment share = spending in `saving` and `invest` categories divided by income.
- Emergency coverage compares emergency fund balance with monthly essential spending. It is displayed as months of cover.
- Debt direction compares repayments with new borrowing; no repayment while loans are open is flagged.
- Budget utilization = spending in attached categories divided by the budget amount for the selected period.

All money arithmetic is integer based. Ratios in check descriptions are floating point only for display. Months are half-open date ranges `[first day, first day of next month)`, which handles leap years without special cases. Missing income or expense data produces an empty result rather than a fabricated score. The exact check thresholds and messages are in `internal/domain/financialhealth/health.go`, with tests beside that file.
