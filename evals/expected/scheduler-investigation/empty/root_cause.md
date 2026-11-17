# A successful empty list is not a test verdict

The Scheduler list succeeds with HTTP 200, `items: []` and only a self link. Say no accessible, unexpired pools were returned for the requested build. There is no continuation to follow and no returned pool UUID to inspect.

Distinguish this successful read from authentication, permission and availability errors. It does not establish that the build never used Scheduler, that no tests exist, that testing finished, or that tests passed. Do not invent pool IDs or repeatedly list the same empty page. No fix is requested.
