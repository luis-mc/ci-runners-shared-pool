# CRSP405 report (PC-bound)

Status: started 28 Nov.

Findings
- ApplyPlan runs the whole plan inside the apply POST; the console awaits it, so its poll loop never runs and the bars jump from 0 to Done. The step state "running" was never saved. A pool on/off plan had one step per switch, so even polling could not show sub-steps.
- Card title wraps in the card border (Card title prop) and covers the bar above.

Plan: backend steps split (pool switch, adjust runners, check), running state saved and published, apply survives a closed page; console posts apply and polls at once, resumes after reload; card title as heading inside card.
