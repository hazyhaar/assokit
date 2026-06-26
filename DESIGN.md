---
spec: DESIGN.md
spec_version: "1.0"
name: assokit
layer: product
---

# assokit — couche produit (design système)

Ce document est la couche produit de l'interface assokit : roster des composants vendorés,
règles d'usage, et patterns spécifiques au forum. Il ne porte PAS la charte graphique
(palette papier-ochre, tokens sémantiques, échelle typographique, 18 refus visuels) : cette
charte est définie dans `/devhoros/templux/DESIGN.md`, source de vérité pour toute session
ou sous-agent. Lire ce fichier avant d'écrire le moindre token de couleur ou valeur CSS.

Principe d'étanchéité : aucun token hexadécimal, aucune valeur de couleur brute, aucune
taille fixe en px ne figure dans ce document ni dans les vues `.templ` d'assokit. Tout
paramètre visuel passe par les variables CSS sémantiques du preset (`--surface`, `--ink`,
`--accent`, `--border`…).

---

## Roster vendoré (11 composants actifs)

Les composants ci-dessous vivent dans `internal/webui/templux/`. Ils sont copiés depuis
`/devhoros/templux/` (vendoring) et ne doivent jamais être modifiés en place : toute
évolution remonte au dépôt amont puis est re-copiée.

| Composant | Signature | Quand l'utiliser |
|---|---|---|
| **Button** | `Button(label, variant, attrs)` | Toute action primaire ou secondaire déclenchant une mutation ; variante `primary` pour l'action principale de l'écran, `ghost` pour les actions secondaires. |
| **Card** | `Card(title, body)` | Conteneur thématique délimité (article, entrée de liste, panneau d'information) portant une identité visuelle propre. Ne pas reproduire son balisage à la main dans une vue. |
| **Badge** | `Badge(label, tone)` | Pastille d'état ou de comptage (non-lu, statut, tag). Tones : `success`, `warning`, `danger`, `info`, `neutral`. |
| **Input** | `Input(name, value, attrs)` | Champ de texte monoligne dans un formulaire ou une barre de recherche. |
| **Textarea** | `Textarea(name, value, rows, attrs)` | Champ de texte multiligne (corps de message, description). |
| **FormField** | `FormField(label, helper, errorMsg, body)` | Enveloppe d'un champ de formulaire : label, texte d'aide, message d'erreur inline, et le champ lui-même passé en `body`. Toujours utiliser pour les formulaires accessibles. |
| **LinkButton** | `LinkButton(label, href, variant, attrs)` | Lien qui ressemble à un bouton ; navigation inter-écrans ou lien externe. |
| **Select** | `Select(name, options, selected, attrs)` | Liste déroulante de choix unique. |
| **Checkbox** | `Checkbox(name, checked, label, attrs)` | Case à cocher dans un formulaire ou une liste d'options. |
| **Alert** | `Alert(severity, message)` | Bannière de notification contextuelle (succès, avertissement, erreur, information). Sévérités : `success`, `warning`, `danger`, `info`. Utiliser pour les états vides explicatifs et les retours d'action. |
| **Progress** | `Progress(current, max, label)` | Barre de progression linéaire pour les opérations longues ou les indicateurs de taux. |

---

## Règle d'usage — [bloc identifiable = composant]

Aucun bloc d'interface identifiable (carte, pastille, item de liste, état vide, fil
d'Ariane, poignée, panneau, en-tête, message…) écrit à la main dans une vue
(`views/*.templ`). Tout bloc porteur d'une identité d'interface est un composant
nommé : promu au roster partagé `internal/webui/templux/` s'il est transverse,
sinon rangé en composant de feature `internal/webui/components/<feature>/`. Seuls les
conteneurs de pure disposition (wrapper flex/grid sans identité ni style propre)
restent inline. Réflexe obligatoire : inventaire du roster AVANT tout markup. Refus
mécanique : balisage structurel identifiable anonyme dans une vue.
[bloc identifiable = composant]

### Application pratique

Avant d'écrire du balisage dans une vue :

1. Consulter le roster ci-dessus (11 composants actifs).
2. Consulter `internal/webui/components/<feature>/` pour les composants de feature déjà
   extraits.
3. Si le bloc voulu n'existe pas encore : le créer en composant nommé avant de l'utiliser
   dans la vue. Transverse → `internal/webui/templux/` ; propre à une feature → le
   sous-dossier correspondant.
4. Seule exception : les wrappers `<div class="flex …">` sans identité d'interface ni style
   propre (rôle purement structurel, aucun comportement, aucun style de fond/bord/ombre)
   peuvent rester inline.

---

## Patterns assokit — composants à venir (refactor forum)

Les motifs ci-dessous ont été identifiés lors de l'audit de `views/forum.templ`
(432 lignes, 65 blocs porteurs d'une classe). Ils seront produits lors du refactor forum
(goal normalisation UI forum). Leurs signatures définitives seront fixées par l'agent de
refactor ; ce document les documente comme existants conceptuellement.

### Motifs promus dans le roster partagé (`internal/webui/templux/`)

Ces cinq motifs sont transverses (réutilisables hors forum) et rejoindront le roster partagé :

| Motif | Description fonctionnelle |
|---|---|
| `PageHeader(subtitle, title, actions)` | En-tête de page avec titre, sous-titre et zone d'actions contextuelles (boutons, liens). Utilisé en tête de toute vue principale. |
| `ResizableStation(panels)` | Conteneur de disposition à panneaux redimensionnables disposés côte à côte. Porte la logique de glisser-déposer des séparateurs. |
| `ResizablePanel(title, id, width, body)` | Panneau individuel au sein d'une station redimensionnable ; porte son titre, son identifiant de redimensionnement et son corps scrollable. |
| `CollapsibleHeader(title)` | En-tête cliquable ouvrant/fermant une section. Utilisable dans les panneaux de navigation latérale et les groupes de liste. |
| `MessageCard(author, timestamp, body, tone)` | Carte de message auteur/date/corps avec variante de ton (neutre, réponse, citation). Utilisée pour les questions et réponses du forum. |

### Composants de feature forum (`internal/webui/components/forum/`)

Les ~20 blocs extraits de `views/forum.templ` non couverts par le roster partagé seront
rangés ici. Exemples attendus : poignées de redimensionnement, corps de panneau scrollable,
items de liste cliquables (catégorie, question, branche), états vides textuels, fil
d'Ariane, séparateur, label upload fichier, indentation de nesting, section full-bleed.
Leurs signatures exactes seront produites lors du refactor ; ce dossier est à créer.
