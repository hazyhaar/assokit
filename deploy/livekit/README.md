# Serveur média LiveKit pour la visioconférence assokit

assokit ne relaie pas lui-même les flux audio/vidéo : il **délègue à un serveur
LiveKit** et se contente de forger les jetons d'accès aux salles. Ce dossier
fournit un déploiement auto-hébergé minimal, souverain (aucun service tiers).

**Une communauté = un serveur LiveKit**, partagé par tous ses membres — jamais
un serveur par membre. Les **membres n'installent rien** : ils ouvrent un lien
`/visio/<salle>` dans leur navigateur et sont connectés. Tout le setup décrit
ici concerne **l'administrateur**, une fois.

## Prérequis

- Docker + le plugin `docker compose`.
- Une machine Linux pour le `network_mode: host` du compose (voir §4 pour
  macOS/Windows).
- Les ports ouverts au pare-feu : `7880/tcp` (signal), `7881/tcp` (repli média),
  `50000-60000/udp` (média WebRTC).

## 1. Générer la paire clé/secret

LiveKit signe les jetons avec une paire `api_key` / `api_secret`. En générer une :

```sh
docker run --rm livekit/livekit-server:v1.8 generate-keys
```

La commande affiche une `API Key` et un `API Secret`. Reporter ces deux valeurs
dans `livekit.yaml`, section `keys`, en remplaçant les placeholders :

```yaml
keys:
  <API Key générée>: <API Secret généré>
```

Le secret doit faire au moins 32 caractères (la commande s'en assure). Ne jamais
committer un `livekit.yaml` contenant de vraies clés.

## 2. Démarrer le serveur

```sh
cd deploy/livekit
docker compose up -d
docker compose logs -f   # vérifier « starting LiveKit server »
```

Le serveur écoute alors sur `ws://<hôte>:7880`.

## 3. Brancher assokit sur le serveur

Dans l'interface d'administration d'assokit :

1. ouvrir **`/admin/connectors`** ;
2. choisir **LiveKit (visioconférence)** → **Configurer** ;
3. renseigner les trois champs :
   - `server_url` : `ws://<hôte>:7880` (ou `wss://<domaine>` derrière TLS, §4) ;
   - `api_key` : la clé générée en §1 ;
   - `api_secret` : le secret généré en §1 ;
4. valider. Les trois valeurs sont **chiffrées dans le Vault** d'assokit ; le
   secret n'apparaît plus jamais en clair.

Vérification : ouvrir `/visio/test` dans un navigateur — la salle doit se
connecter. À deux onglets/appareils, les participants se voient.

## 4. Production : TLS et NAT

Le compose par défaut convient à un réseau local (même machine, LAN, VPN). Pour
un serveur exposé sur Internet, deux ajustements :

- **TLS (wss\://).** Les navigateurs refusent un `ws://` non chiffré depuis une
  page servie en `https://`. Placer un reverse-proxy TLS (Caddy, nginx) devant
  le port `7880` et utiliser `wss://<domaine>` comme `server_url`. Caddy en deux
  lignes : `<domaine> { reverse_proxy localhost:7880 }`.
- **NAT (VPS/cloud).** Si le serveur est derrière un NAT (IP privée), passer
  `use_external_ip: true` dans `livekit.yaml` pour que LiveKit annonce son IP
  publique dans les négociations WebRTC. En réseau local, laisser `false`.

Un déploiement public robuste (TURN dédié, scaling multi-nœuds, fédération
inter-communautés) dépasse ce dossier de démarrage et relève d'un lot infra
séparé.

## Désactivation

La route gardée `/salon/{slug}/visio` (réservée aux membres du salon, visio
activée) n'est fonctionnelle que si le connecteur LiveKit est configuré (secret
présent dans le Vault). Sans configuration, assokit fonctionne normalement, la
visio simplement absente. Pour arrêter le serveur média :

```sh
docker compose down
```
