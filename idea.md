- a go-links directory (DB + Web) that allows users to register go-links and search for go-links
- a go-links chrome extension, that allows users to use the go-links registered in the directory via "go/..." links in the browser address bar.


- links support variables, with rich support in the directory web — e.g. /go/reports/{month}/{day} -> http://reports_url?month={month}&day={day}
- variables can have (don't have to, but can) a type — e.g. {month:number} or {month:regex(^(0?[1-9]|1[0-2])$)} and so on. If the go-link url doesnt match the variable types, it should be treated as a missed one. 
- users can edit their links in the directory web
- if a user provided an unknown go link, we should check if there is any short distance similar link and ask the user "did you mean....?"
- when there is a match, user get redirected to the url 


Solution must be runnable locally, but also in a setup, where the directory is in deployed somewhere (TBD). 