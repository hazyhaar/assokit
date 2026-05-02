# Connecter votre association à HelloAsso

Permet de recevoir les dons et adhésions HelloAsso directement dans le tableau de bord de votre site, sans devoir aller chercher l'info chez HelloAsso à chaque fois.

**Temps : 15 minutes. Difficulté : facile si vous savez copier-coller.**

> Si vous bloquez à n'importe quelle étape, il y a une section **Si vous bloquez** à la fin de cette page.

## Avant de commencer

Vous avez besoin de :

- ✓ Un compte HelloAsso actif pour votre association.
- ✓ L'accès admin à votre site (vous devez pouvoir vous connecter sur `https://votre-site.org/admin/panel`).
- ✓ Une feuille de papier ou un gestionnaire de mots de passe pour noter 3 codes secrets pendant la procédure.

## Étape 1 — Créer une « application » dans HelloAsso

Une « application » HelloAsso est une autorisation que vous donnez à votre site pour parler avec HelloAsso. Elle vous donne deux codes secrets que vous copierez ensuite dans votre site.

1. Connectez-vous sur `https://www.helloasso.com` et allez dans l'espace de votre association.
2. Cliquez sur **Paramètres** → **Intégrations & API** → **Applications**.

   ![Capture 1 — Menu Intégrations](img/helloasso/01-menu-integrations.png)
   <!-- TODO captures à produire en interne — décrites textuellement ci-dessus -->

3. Cliquez sur le bouton **Créer une application**.
4. Donnez-lui un nom : par exemple « Mon site web ». Le nom n'a pas d'importance, c'est juste pour vous y retrouver plus tard.
5. Une fois l'application créée, vous voyez deux codes :
   - **Client ID** : commence par des lettres et des chiffres (ex. `abcd1234`).
   - **Client Secret** : longue chaîne aléatoire. **Copiez-la dès maintenant** : HelloAsso ne vous la montrera plus une fois la fenêtre fermée.

   ![Capture 2 — Credentials](img/helloasso/02-credentials.png)
   <!-- TODO capture -->

   Notez les deux codes sur votre papier ou dans votre gestionnaire de mots de passe.

## Étape 2 — Configurer dans votre site

1. Connectez-vous sur `https://votre-site.org/admin/connectors/helloasso` (remplacez par votre vraie adresse de site).
2. Vous voyez un formulaire avec deux champs : **Client ID** et **Client Secret**. Collez les deux codes copiés à l'étape précédente.

   ![Capture 3 — Configuration admin](img/helloasso/03-admin-config.png)
   <!-- TODO capture -->

3. Cochez la case **Mode sandbox** SI vous voulez d'abord tester sans vraies transactions. Sinon laissez décoché.
4. Cliquez **Enregistrer**.
5. Cliquez ensuite sur **Tester la connexion**. Si vous voyez « ✓ Connecté », tout va bien. Sinon, vérifiez que vos deux codes sont bien recopiés sans espace ni faute.

## Étape 3 — Configurer les webhooks (notifications HelloAsso → votre site)

C'est ce qui permet à votre site d'être notifié automatiquement quand quelqu'un fait un don ou adhère via HelloAsso. Sans cette étape, vous devrez aller voir HelloAsso pour savoir si vous avez reçu un don — avec cette étape, le don apparaît tout seul dans votre tableau de bord.

1. Retournez sur HelloAsso → **Paramètres** → **Intégrations & API** → **Webhooks**.
2. Cliquez **Ajouter un webhook**.
3. **URL du webhook** : tapez ou collez `https://votre-site.org/webhooks/helloasso` (remplacez par votre vraie adresse). Faites attention : c'est `https://`, pas `http://`. Pas d'espace, pas de typo.
4. **Événements** : cochez « Tous les événements », ou au minimum :
   - `Order.Notification`
   - `Payment.Notification`
   - `Payment.Refunded`
5. **Code de signature** : HelloAsso vous donne un code secret unique pour ce webhook. **Copiez-le**.

   ![Capture 4 — Webhook setup](img/helloasso/04-webhook-setup.png)
   <!-- TODO capture -->

6. Retournez sur votre site → `https://votre-site.org/admin/connectors/helloasso`.
7. Trouvez le champ **Webhook Signing Secret** et collez le code copié à l'étape 5.
8. Cliquez **Enregistrer**.

## Étape 4 — Vérifier que tout fonctionne

1. Sur HelloAsso, faites un don de test (par exemple 1 €) à votre propre association.
2. Patientez 1 à 2 minutes (HelloAsso a un petit délai).
3. Sur votre site, allez sur `https://votre-site.org/admin/donations`.
4. Vous devez voir le don apparaître dans la liste, avec le montant 1 €, le nom du donateur (vous-même), et le statut « payé ».

Si rien n'apparaît au bout de 5 minutes :

- Vérifiez que l'URL webhook est bien `https://votre-site.org/webhooks/helloasso` exactement (pas `http`, pas de point manquant, pas d'espace).
- Allez dans HelloAsso → **Paramètres** → **Webhooks** → onglet **Logs**. Vous voyez l'historique des notifications envoyées et si elles ont été reçues correctement par votre site.
- Si vous voyez une erreur « 401 Unauthorized » dans les logs HelloAsso, cela veut dire que le code de signature ne correspond pas. Refaites l'étape 3 point 5 et 7 attentivement.

## Si vous bloquez

Vous avez plusieurs recours :

- **Demander à un proche** qui s'y connaît un peu en informatique de regarder cette page avec vous — la procédure est principalement du copier-coller, et avoir quelqu'un à côté aide souvent.
- **Écrire à votre administrateur de plateforme** (par exemple `contact@nonpossumus.eu`) en précisant à quelle étape vous bloquez et quel message d'erreur vous voyez. Plus vous donnez de détails, plus la réponse est rapide.
- **Désactiver temporairement** le connecteur dans `https://votre-site.org/admin/connectors`. Décochez la case **Activé** et cliquez **Enregistrer**. Vos boutons de don continueront à fonctionner en « mode lien sortant » : les dons se font sur HelloAsso et vous les voyez en allant sur HelloAsso. C'est moins pratique mais ça marche tout de suite.

> **Bon à savoir** : même sans le connecteur configuré, votre page `https://votre-site.org/soutenir` reste fonctionnelle. Les visiteurs cliquent sur « Faire un don », sont envoyés sur HelloAsso, et vous voyez les dons sur votre tableau de bord HelloAsso comme avant. Le connecteur sert uniquement à rapatrier l'info en plus dans VOTRE tableau de bord.

## URL publique du webhook

Pour information technique : l'URL `https://votre-site.org/webhooks/helloasso` est **publique** par design. C'est HelloAsso qui appelle votre site (pas l'inverse), donc cette URL ne peut pas être derrière un mot de passe. La sécurité est assurée par le **Code de signature** de l'étape 3 : si quelqu'un essaie d'envoyer un faux don sans connaître ce code, votre site le rejette automatiquement.
