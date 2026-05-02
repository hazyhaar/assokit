# Contributing to assokit

## Style Go

Lire [`CODE_PATTERNS_GO.md`](CODE_PATTERNS_GO.md) avant toute contribution.
9 sections : Naming, Errors, Returns, Struct literals, Defer, Tests, Templ, Comments, Imports — chacune avec snippet DO/DON'T.

## Workflow PR

1. Fork + branche feature depuis `main` (`git checkout -b feat/ma-feature`)
2. Commits atomiques avec message format `type(scope): description` (feat/fix/refactor/docs/test)
3. `CGO_ENABLED=0 go build ./...` → exit 0 avant d'ouvrir la PR
4. `CGO_ENABLED=0 go test ./...` → exit 0 avant d'ouvrir la PR
5. `CGO_ENABLED=0 go test -tags=integration_cdp ./...` → exit 0 ou skip explicite si Chromium absent
6. Ouvrir la PR sur `main`, décrire le POURQUOI (pas le QUOI que le diff montre déjà)

## Pré-merge requirements

- [ ] `go test ./...` all green (aucun FAIL)
- [ ] Nouveau code couvert par au moins un test gardien
- [ ] Pas de `panic` hors init, pas de `log.Fatal` hors `main`
- [ ] Imports groupés (3 blocs : stdlib / external / local)
- [ ] Pas de secret ou chemin de machine hardcodé

## Règle bloquante : NO-DEPLOY-WITHOUT-LOCALHOST-CDP-E2E-ALL-GREEN

Aucun deploy en production sans exécuter et valider les 5 étapes pré-deploy :

1. `CGO_ENABLED=0 go build ./...` → exit 0
2. `CGO_ENABLED=0 go test ./...` → exit 0
3. `CGO_ENABLED=0 go test -tags=integration_cdp ./...` → exit 0 (ou skip *explicite*)
4. Smoke localhost : démarrer le binaire, `curl` 5+ routes critiques
5. `md5sum` binaire compilé HEAD == binaire à déployer

Si l'un de ces 5 échoue : **deploy bloqué**. Voir `plans/SPRINT0_DEPLOY_RUNBOOK.md` pour le détail.

## Structure du repo

```
cmd/assokit/          — point d'entrée binaire
pkg/api/              — surface publique (New, Options, ListenAndServe, Handler)
pkg/horui/            — UI server-side (middleware, auth, perms, rbac, templ)
internal/             — handlers HTTP, chassis DB, mailer
examples/             — exemple minimal d'une app assokit
```

## Questions / bugs

Ouvrir une issue GitHub avec : version (`git describe --tags`), OS, Go version, output complet de l'erreur.
