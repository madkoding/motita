package i18n

// The phone notifications the gateway pushes (internal/gateway/push.go) in Spanish: the same words
// as the browser's toast for each step of a pull request's watch. Keys are the English text exactly
// as the gateway writes it; a format keeps its verbs, in the same order, in the translation.
var esPush = map[string]string{
	"Motita notifications work":             "Las notificaciones de Motita funcionan",
	"This is how a CI that ends will look.": "Así se verá cuando termine un CI.",
	"CI passed":                             "CI aprobado",
	"Pull request %s is green: you can go and merge it.": "El pull request %s está en verde: ya puedes " +
		"ir a mergearlo.",
	"CI failed": "El CI falló",
	"Motita is fixing it — attempt %d of %d.": "Motita lo está arreglando — intento %d de %d.",
	"New review comments":                     "Nuevos comentarios de revisión",
	"Motita is answering the comments on pull request %s — attempt %d of %d.": "Motita está atendiendo los " +
		"comentarios del pull request %s — intento %d de %d.",
	"Merge conflict": "Conflicto de merge",
	"Pull request %s conflicts with its base branch: Motita is resolving it — attempt %d of %d.": "El pull " +
		"request %s tiene conflictos con su rama base: Motita lo está resolviendo — intento %d de %d.",
	"Motita could not fix the CI": "Motita no pudo arreglar el CI",
	"It tried %d times. Look at pull request %s and try again when you are ready.": "Lo intentó %d veces. " +
		"Revisa el pull request %s y vuelve a intentarlo cuando quieras.",
	"This repository has no CI":                  "Este repositorio no tiene CI",
	"Pull request %s has no checks to wait for.": "El pull request %s no tiene checks que esperar.",
	"The CI is red on the base branch too":       "El CI también está en rojo en la rama base",
	"Pull request %s fails the same checks as the branch it merges into: this change did not cause it.": "El " +
		"pull request %s falla los mismos checks que la rama en la que se mergea: este cambio no lo causó.",
	"Pull request closed":                              "Pull request cerrado",
	"Pull request %s was closed without being merged.": "El pull request %s se cerró sin mergearse.",
	"Pull request merged":                              "Pull request mergeado",
	"Pull request %s was merged.":                      "El pull request %s fue mergeado.",
}

func init() { Register(ES, esPush) }
