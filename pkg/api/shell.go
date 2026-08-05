// shell.go — surface publique de la coquille applicative socle.
// Expose le shell templux complet (nav, user-menu, flashs, FeedbackWidget, assets)
// aux instances de bordure (ex. DEMOAPP) sans qu'elles importent internal/webui, qui
// leur est interdit par la frontiere Go. La surface delegue au shell interne :
// aucune seconde coquille n'est recreee.
package api

import (
	"github.com/a-h/templ"

	"github.com/hazyhaar/assokit/internal/webui"
)

// Shell rend une page dans la coquille socle standard (layout papier-ochre centre,
// max-w-5xl). content est le corps de la <main>. Nav, user-menu, flashs et
// FeedbackWidget sont ceux du socle, alimentes par la chaine de middlewares montee
// avant le hook RegisterRoutes (Auth, Flash, theme).
func Shell(title string, content templ.Component) templ.Component {
	return webui.Shell(title, content)
}

// ShellCustom rend une page dans la coquille socle avec trois surcharges pour une
// disposition propre a l'instance (ex. carte plein ecran) :
//   - extraHead : composant injecte en fin de <head> (assets propres, ex. MapLibre) ;
//     nil = aucun ajout ;
//   - bodyClass : classe du <body> (ex. "min-h-screen flex flex-col" pour le plein
//     ecran) ; vide = body socle nu ;
//   - mainClass : classe de la <main>.
//
// Nav, flashs et FeedbackWidget du socle restent en place : c'est le shell UNIQUE,
// pas une coquille parallele.
func ShellCustom(title string, extraHead templ.Component, bodyClass, mainClass string, content templ.Component) templ.Component {
	return webui.ShellCustom(title, extraHead, bodyClass, mainClass, content)
}
