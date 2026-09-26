{{range $i, $page := .Pages}}=== page {{add $i 1}} ===
{{$page}}
{{end -}}
