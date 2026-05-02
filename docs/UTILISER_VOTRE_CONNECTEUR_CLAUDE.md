# Piloter votre site associatif via Claude

Vous êtes membre de votre association mais vous galérez avec les formulaires
web ? Vous pouvez utiliser **Claude** (claude.ai) pour faire vos actions à
votre place, en parlant en langage naturel.

> **Temps d'installation : 5 minutes. Aucune compétence technique requise.**

## Avant de commencer

Vous avez besoin :

- ✓ Un compte Claude Pro ou Max sur [claude.ai](https://claude.ai).
- ✓ Une adresse email enregistrée sur votre site associatif (ex
  nonpossumus.eu). Si vous n'en avez pas encore, il suffit de l'entrer à
  l'étape 4 — un compte sera créé automatiquement.
- ✓ Aucun mot de passe à retenir : tout passe par votre email.

## Étape 1 — Aller dans Settings de Claude

1. Connectez-vous sur [claude.ai](https://claude.ai).
2. Cliquez sur l'icône ⚙ (Settings) en bas à gauche.
3. Choisissez **Connectors** dans le menu.

## Étape 2 — Ajouter votre site comme Custom Connector

1. Cliquez sur **Add Custom Connector**.
2. Entrez l'URL : `https://VOTRE-SITE.org/mcp` (remplacer VOTRE-SITE par
   l'adresse de votre association, ex `https://nonpossumus.eu/mcp`).
3. Cliquez **Add**.

## Étape 3 — Vous identifier sur votre site

Une page s'ouvre dans votre navigateur. C'est votre site associatif qui vous
demande votre email pour vous identifier.

1. Entrez votre email.
2. Cliquez **Recevoir le lien**.
3. Allez dans votre boîte mail (vérifiez les spams si besoin).
4. Cliquez sur le lien reçu (valable 15 minutes).

## Étape 4 — Donner votre accord

Une page apparaît qui résume ce que Claude pourra faire en votre nom.
Exemple :

> L'application **Anthropic Claude Web** souhaite faire des actions à votre
> place sur Nonpossumus.
>
> Elle pourra :
> - ✓ Lire les feedbacks publiés sur le site
> - ✓ Publier un message sur le forum en votre nom
> - ✓ Modifier votre propre profil
>
> Elle ne pourra PAS :
> - ✗ Voir votre mot de passe (vous n'en avez pas)
> - ✗ Modifier les comptes des autres membres
> - ✗ Faire des dons en votre nom

Cliquez **Autoriser** si vous êtes d'accord.

## Étape 5 — C'est tout !

Retournez sur claude.ai et parlez à Claude normalement. Exemples de phrases :

- *"Lis-moi les derniers messages du forum."*
- *"Publie un message sur le forum disant que je serai présent à l'AG du 15."*
- *"Triater les feedbacks reçus cette semaine."*
- *"Quels sont les paliers de don configurés sur le site ?"*
- *"Modifie ma bio pour mettre 'Bénévole depuis 2020'."*

Claude fera les actions et vous résumera les résultats en français.

## Sécurité — points importants

- **Aucun mot de passe n'est stocké**. Toute connexion passe par un lien
  reçu par email (le lien est valide 15 minutes maximum, à usage unique).
- **Vous pouvez révoquer l'accès à tout moment** : retournez dans Settings
  → Connectors → cliquez sur la corbeille à côté du connecteur.
- **Claude ne peut faire QUE les actions que vous avez autorisées** sur la
  page d'accord. Vous pouvez toujours revoir cette liste en demandant à
  Claude *"qu'est-ce que tu peux faire pour moi sur le site ?"*.
- **Vos données restent sur votre serveur** (nonpossumus.eu). Claude ne les
  copie pas, il les lit/modifie en passant par votre site.

## En cas de problème

**"Je n'ai pas reçu l'email"**
→ Vérifiez les spams. Sinon, redemandez un lien (3 demandes max par 15
minutes).

**"Le lien dit 'expiré'"**
→ Les liens sont valables 15 minutes. Redemandez-en un.

**"Le lien dit 'déjà utilisé'"**
→ Chaque lien ne fonctionne qu'une fois (sécurité). Redemandez-en un.

**"Claude dit 'je n'ai pas accès à cette action'"**
→ L'action n'est peut-être pas dans la liste autorisée. Demandez à Claude
quelles sont ses permissions, ou ré-autorisez le connecteur (Settings →
Connectors → Reconnect).

## Cas d'usage typiques

### Pour un adhérent
- Voir son historique d'adhésion / dons.
- Modifier ses coordonnées.
- Participer au forum sans aller naviguer dans l'interface web.

### Pour un modérateur
- Trier les feedbacks reçus.
- Modérer le forum (avertir, suspendre temporairement, supprimer un message).
- Lire les emails envoyés par le site.

### Pour un admin
- Modifier la configuration du site (couleurs, textes, logos).
- Voir la liste des dons reçus.
- Configurer les intégrations tierces (HelloAsso, etc.).
- Gérer les rôles des autres membres.

Toutes ces actions restent disponibles via l'interface admin web classique
(`/admin/panel`) — le connecteur Claude est un raccourci pour ceux qui
préfèrent dialoguer en langage naturel.

## Pour les développeurs / curieux

Le connecteur utilise le protocole standard **MCP (Model Context Protocol)**
avec OAuth 2.1 + PKCE + Dynamic Client Registration (RFC 7591). Aucune
configuration manuelle de client_id n'est nécessaire — claude.ai s'enregistre
automatiquement à la première utilisation.

Endpoints exposés :
- `/.well-known/oauth-authorization-server` — discovery RFC 8414.
- `/oauth2/register` — DCR public.
- `/oauth2/authorize` — auth code flow + PKCE S256.
- `/oauth2/token` — exchange code → access_token.
- `/mcp` — endpoint MCP Streamable HTTP avec Bearer token.

Code source : [github.com/hazyhaar/assokit](https://github.com/hazyhaar/assokit).
