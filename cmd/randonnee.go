package cmd

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

const lccRandoURL = "https://www.liffre-cormier.fr/tourisme/les-activites-de-pleine-nature/randonnees-pedestres"
const cirkwiGPXFmt = "https://www.cirkwi.com/fr/exporter-gpx/%s_0?mb_use_temp=1"
const visuGPXCommuneFmt = "https://www.visugpx.com/itineraires/randonnee-pedestre/ille-et-vilaine/%s/"
const visuGPXDownloadFmt = "https://www.visugpx.com/download.php?id=%s"

// Sitemaps Cirkwi publics : on y trouve tous les circuits avec leur slug
// descriptif. Filtré par les 9 communes de Liffré-Cormier, c'est notre source
// la plus fiable pour retrouver la fiche officielle d'un circuit.
var cirkwiSitemapURLs = []string{
	"https://www.cirkwi.com/sitemap/circuit_0.xml",
	"https://www.cirkwi.com/sitemap/circuit_1.xml",
	"https://www.cirkwi.com/sitemap/circuit_2.xml",
	"https://www.cirkwi.com/sitemap/circuit_3.xml",
}

// Slugs des 9 communes de Liffré-Cormier tels qu'ils apparaissent dans les
// URLs Cirkwi.
var communeSlugs = []string{
	"chasne-sur-illet", "dourdain", "erce-pres-liffre", "gosne",
	"la-bouexiere", "liffre", "livre-sur-changeon",
	"mezieres-sur-couesnon", "saint-aubin-du-cormier",
}

// overrides force une URL GPX précise pour un libellé LCC, quand aucune des
// sources automatiques ne donne un résultat fiable. La clé est le libellé
// exact tel qu'il apparaît dans l'accordéon de la page LCC.
var overrides = map[string]string{
	// Aucun match fiable dans les sources automatiques : la fiche Cirkwi
	// homonyme n'existe pas et le sitemap retombe sur un circuit VTT de
	// Vieux-Vy-sur-Couesnon. Trace utilisateur VisuGPX validée à la main.
	"Balade au Pays du Couesnon à Mézières-sur-Couesnon": "https://www.visugpx.com/download.php?id=r0QmgG7RKG",
}

// Mots-clés trop courants pour discriminer un circuit lors d'un matching par
// libellé. Permet d'éliminer le bruit ("la", "à", "vers", …).
var stopWords = map[string]bool{
	"a": true, "au": true, "aux": true, "le": true, "la": true, "les": true,
	"l": true, "d": true, "de": true, "des": true, "du": true, "et": true,
	"ou": true, "en": true, "vers": true, "sur": true, "sous": true, "par": true,
	"pour": true, "entre": true, "dans": true, "chez": true, "circuit": true,
	"balade": true, "randonnee": true, "rando": true,
}

var randonneeOutputDir string

func init() {
	randonneeCmd.Flags().StringVarP(&randonneeOutputDir, "output", "o", "randonnees", "répertoire de destination des fichiers GPX")
	rootCmd.AddCommand(randonneeCmd)
}

var randonneeCmd = &cobra.Command{
	Use:   "randonnee",
	Short: "Télécharge les traces GPX des circuits pédestres référencés par Liffré-Cormier",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRandonnee(randonneeOutputDir)
	},
}

type circuit struct {
	libelle   string
	cirkwiID  string // vide si pas de lien Cirkwi
	ivTourURL string // page Ille-et-Vilaine Tourisme associée (vide sinon)
}

// accordionRE délimite chaque bloc de circuit dans la page : le titre cliquable
// (class="accordion-toggle") sert de séparateur naturel.
var accordionRE = regexp.MustCompile(`(?s)<a[^>]*class="accordion-toggle[^"]*"[^>]*>(.*?)</a>`)
var cirkwiURLRE = regexp.MustCompile(`https://www\.cirkwi\.com/fr/circuit/(\d+)-[A-Za-z0-9\-]+`)
var ivTourURLRE = regexp.MustCompile(`https://www\.ille-et-vilaine-tourisme\.bzh/[^"'\s]+`)
var ivTourGPXRE = regexp.MustCompile(`https://[^"'\s]+\.gpx`)
var tagRE = regexp.MustCompile(`<[^>]+>`)
var spaceRE = regexp.MustCompile(`\s+`)

func extractCircuits(page string) []circuit {
	// On découpe la page sur chaque accordion-toggle : la séquence est
	// [pré, titre1, contenu1, titre2, contenu2, ...].
	idx := accordionRE.FindAllStringSubmatchIndex(page, -1)
	if len(idx) == 0 {
		return nil
	}
	out := make([]circuit, 0, len(idx))
	for i, m := range idx {
		titleHTML := page[m[2]:m[3]]
		title := strings.TrimSpace(spaceRE.ReplaceAllString(tagRE.ReplaceAllString(titleHTML, " "), " "))
		title = html.UnescapeString(title)
		if title == "" {
			continue
		}
		// Contenu = du titre courant jusqu'au début du suivant (ou fin).
		contentEnd := len(page)
		if i+1 < len(idx) {
			contentEnd = idx[i+1][0]
		}
		content := page[m[1]:contentEnd]
		var cid, ivt string
		if cm := cirkwiURLRE.FindStringSubmatch(content); cm != nil {
			cid = cm[1]
		}
		if im := ivTourURLRE.FindString(content); im != "" {
			ivt = im
		}
		out = append(out, circuit{libelle: title, cirkwiID: cid, ivTourURL: ivt})
	}
	return out
}

// slugify produit un nom de fichier raisonnable à partir d'un libellé Unicode.
func slugify(s string) string {
	repl := strings.NewReplacer(
		"à", "a", "â", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"î", "i", "ï", "i",
		"ô", "o", "ö", "o",
		"ù", "u", "û", "u", "ü", "u",
		"ç", "c",
		"À", "a", "Â", "a", "Ä", "a",
		"É", "e", "È", "e", "Ê", "e", "Ë", "e",
		"Î", "i", "Ï", "i",
		"Ô", "o", "Ö", "o",
		"Ù", "u", "Û", "u", "Ü", "u",
		"Ç", "c",
		"’", "'", "‘", "'",
	)
	s = strings.ToLower(repl.Replace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func runRandonnee(outputDir string) error {
	fmt.Fprintln(os.Stderr, "Récupération de la page randonnées :", lccRandoURL)
	body, err := httpGetBody(lccRandoURL)
	if err != nil {
		return err
	}

	circuits := extractCircuits(string(body))
	if len(circuits) == 0 {
		return fmt.Errorf("aucun circuit trouvé sur la page")
	}
	fmt.Fprintf(os.Stderr, "Circuits référencés : %d\n", len(circuits))

	fmt.Fprintln(os.Stderr, "Chargement de l'index Cirkwi (sitemaps)...")
	local, all, err := loadCirkwiSitemap()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  avertissement : index Cirkwi indisponible (%v)\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "  %d circuits sur les 9 communes (+ %d hors zone en repli strict)\n", len(local), len(all))
	}
	cirkwiSitemapCache = local
	cirkwiSitemapAll = all

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("création répertoire : %w", err)
	}

	var ok, missing int
	for _, c := range circuits {
		dest := filepath.Join(outputDir, slugify(c.libelle)+".gpx")
		source, err := fetchTrace(c, dest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [skip]  %s (%v)\n", c.libelle, err)
			missing++
			continue
		}
		fmt.Fprintf(os.Stderr, "  [ok]    %s -> %s (via %s)\n", c.libelle, filepath.Base(dest), source)
		ok++
	}
	fmt.Fprintf(os.Stderr, "Téléchargés : %d / %d (%d sans trace automatiquement récupérable)\n",
		ok, len(circuits), missing)
	return nil
}

// cirkwiSitemapCache contient les circuits Cirkwi dont le slug mentionne une
// des 9 communes de Liffré-Cormier (priorité absolue pour le matching).
// cirkwiSitemapAll contient tous les autres circuits du sitemap : utilisé en
// second recours avec un seuil strict, pour rattraper les fiches dont le slug
// n'inclut pas la commune (cas "les-rotes-du-hen-herveleu").
var (
	cirkwiSitemapCache []cirkwiCandidate
	cirkwiSitemapAll   []cirkwiCandidate
)

type cirkwiCandidate struct {
	id     string
	slug   string
	tokens map[string]bool
}

// fetchTrace essaie successivement les sources GPX connues pour un circuit :
// override manuel, Cirkwi (lien LCC explicite), Cirkwi (recherche par sitemap),
// IV-Tourisme, VisuGPX, Visorando, Wikiloc. Écrit le GPX dans dest dès qu'une
// source répond avec un fichier GPX valide.
func fetchTrace(c circuit, dest string) (string, error) {
	if u, ok := overrides[c.libelle]; ok {
		if err := downloadGPX(u, dest); err == nil {
			return "override (" + u + ")", nil
		}
	}
	if c.cirkwiID != "" {
		if err := downloadGPX(fmt.Sprintf(cirkwiGPXFmt, c.cirkwiID), dest); err == nil {
			return "Cirkwi", nil
		}
	}
	if id, slug, score, weak, err := searchCirkwiSitemap(c.libelle); err == nil {
		if err := downloadGPX(fmt.Sprintf(cirkwiGPXFmt, id), dest); err == nil {
			tag := fmt.Sprintf("Cirkwi sitemap (%d, %s)", score, slug)
			if weak {
				tag = "Cirkwi sitemap FAIBLE — " + tag
			}
			return tag, nil
		}
	}
	if c.ivTourURL != "" {
		if gpxURL, err := findIVTourGPX(c.ivTourURL); err == nil {
			if err := downloadGPX(gpxURL, dest); err == nil {
				return "Ille-et-Vilaine Tourisme", nil
			}
		}
	}
	if hit, err := searchVisuGPX(c.libelle); err == nil {
		if err := downloadGPX(hit.url, dest); err == nil {
			tag := fmt.Sprintf("VisuGPX (%d/%d, « %s »)", hit.score, hit.maxScore, hit.label)
			if hit.score < 2 {
				tag = "VisuGPX FAIBLE — " + tag
			}
			return tag, nil
		}
	}
	// Visorando et Wikiloc cataloguent leurs fiches via du JS et imposent un
	// compte pour le téléchargement GPX. On tente quand même un appel pour
	// matérialiser la source, mais elle échouera en pratique.
	if _, err := searchVisorando(c.libelle); err == nil {
		// (jamais atteint en pratique : pas de fiches en HTML statique)
		return "Visorando", nil
	}
	if _, err := searchWikiloc(c.libelle); err == nil {
		return "Wikiloc", nil
	}
	return "", fmt.Errorf("aucune source GPX disponible")
}

// splitLibelle sépare "X à Commune" en (titre, commune) — pattern utilisé sur
// la page LCC. La commune sert à cibler la bonne page de listing VisuGPX.
func splitLibelle(libelle string) (title, commune string) {
	parts := regexp.MustCompile(`\s+à\s+`).Split(libelle, 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return libelle, ""
}

// significantTokens normalise et garde les mots-clés discriminants. Les pluriels
// français en -s ou -x sont stemmés vers leur forme courte pour que "landes"
// matche "lande" et "rotes" matche "rote".
func significantTokens(s string) map[string]bool {
	s = slugify(s)
	out := map[string]bool{}
	for _, tok := range strings.Split(s, "-") {
		if len(tok) < 3 || stopWords[tok] {
			continue
		}
		out[stem(tok)] = true
	}
	return out
}

// containsCommune renvoie true si au moins un token significatif de la commune
// LCC apparaît dans le candidat (par token exact ou par sous-chaîne pour les
// noms longs comme "bouexiere" ou "couesnon").
func containsCommune(communeTokens, candTokens map[string]bool, candSlug string) bool {
	for tok := range communeTokens {
		if candTokens[tok] {
			return true
		}
		if len(tok) >= 5 && strings.Contains(candSlug, tok) {
			return true
		}
	}
	return false
}

func stem(tok string) string {
	if len(tok) > 4 && (strings.HasSuffix(tok, "s") || strings.HasSuffix(tok, "x")) {
		return tok[:len(tok)-1]
	}
	return tok
}

type visuHit struct {
	url      string
	label    string
	score    int
	maxScore int
}

// searchVisuGPX charge la page « Itinéraires de randonnée pédestre autour de
// <commune> » et cherche le candidat dont le libellé partage le plus de
// mots-clés avec celui du circuit. Un match de 1 mot-clé suffit (le résultat
// est marqué comme faible côté appelant pour permettre une validation manuelle).
// Bonus de score si un mot-clé du titre LCC apparaît comme sous-chaîne dans le
// libellé candidat — utile pour les variantes singulier/pluriel.
func searchVisuGPX(libelle string) (visuHit, error) {
	title, commune := splitLibelle(libelle)
	if commune == "" {
		return visuHit{}, fmt.Errorf("commune introuvable dans le libellé")
	}
	pageURL := fmt.Sprintf(visuGPXCommuneFmt, slugify(commune))
	body, err := httpGetBody(pageURL)
	if err != nil {
		return visuHit{}, err
	}
	cardRE := regexp.MustCompile(`<a[^>]+href="(/[a-zA-Z0-9]{8,12})"[^>]*>\s*([^<\n]{4,160})`)
	wanted := significantTokens(title)
	if len(wanted) == 0 {
		return visuHit{}, fmt.Errorf("titre sans mot-clé discriminant")
	}
	// La page VisuGPX d'une commune contient aussi des itinéraires des communes
	// voisines : on rejette les candidats dont le libellé ne mentionne pas la
	// commune ciblée (cas typique "Gahard, …" listé sur la page Liffré).
	communeTokens := significantTokens(commune)
	var best visuHit
	best.maxScore = len(wanted)
	for _, m := range cardRE.FindAllSubmatch(body, -1) {
		id := string(m[1])[1:]
		cand := html.UnescapeString(strings.TrimSpace(string(m[2])))
		candSlug := slugify(cand)
		got := significantTokens(cand)
		if !containsCommune(communeTokens, got, candSlug) {
			continue
		}
		score := 0
		for tok := range wanted {
			switch {
			case got[tok]:
				score++
			case len(tok) >= 5 && strings.Contains(candSlug, tok):
				score++
			}
		}
		if score > best.score {
			best.score = score
			best.url = fmt.Sprintf(visuGPXDownloadFmt, id)
			best.label = cand
		}
	}
	if best.score < 1 {
		return visuHit{}, fmt.Errorf("aucune correspondance sur VisuGPX")
	}
	return best, nil
}

// searchVisorando : Visorando ne sert pas son catalogue en HTML statique (la
// liste des fiches est rendue côté client) et conditionne le téléchargement
// GPX à un compte. La fonction reste pour que la source apparaisse dans la
// chaîne ; elle échoue tant qu'aucun GPX public n'est trouvé.
func searchVisorando(libelle string) (string, error) {
	_, commune := splitLibelle(libelle)
	if commune == "" {
		return "", fmt.Errorf("commune introuvable")
	}
	pageURL := fmt.Sprintf("https://www.visorando.com/randonnee-%s.html", slugify(commune))
	body, err := httpGetBody(pageURL)
	if err != nil {
		return "", err
	}
	if !regexp.MustCompile(`href="[^"]*\.gpx"`).Match(body) {
		return "", fmt.Errorf("Visorando : aucun GPX public sur %s", pageURL)
	}
	return "", fmt.Errorf("Visorando : GPX non extractible automatiquement")
}

// searchWikiloc : Wikiloc impose un compte pour télécharger les GPX et son
// catalogue est rendu côté client. Aucune trace n'est accessible en HTTP
// simple ; la fonction est présente pour matérialiser la source.
func searchWikiloc(libelle string) (string, error) {
	return "", fmt.Errorf("Wikiloc : téléchargement GPX réservé aux comptes connectés")
}

// cirkwiSitemapURLRE matche les balises <loc> des sitemaps circuit_*.xml.
var cirkwiSitemapURLRE = regexp.MustCompile(`<loc>https://www\.cirkwi\.com/fr/circuit/(\d+)-([a-z0-9-]+)</loc>`)

// loadCirkwiSitemap télécharge les sitemaps Cirkwi et sépare les circuits en
// deux groupes : ceux dont le slug mentionne une commune Liffré-Cormier
// (matching standard) et les autres (matching strict en repli).
func loadCirkwiSitemap() (local, all []cirkwiCandidate, err error) {
	seen := map[string]bool{}
	for _, u := range cirkwiSitemapURLs {
		body, herr := httpGetBody(u)
		if herr != nil {
			return nil, nil, fmt.Errorf("téléchargement %s : %w", u, herr)
		}
		for _, m := range cirkwiSitemapURLRE.FindAllSubmatch(body, -1) {
			id, slug := string(m[1]), string(m[2])
			if seen[id] {
				continue
			}
			seen[id] = true
			cand := cirkwiCandidate{
				id:     id,
				slug:   slug,
				tokens: significantTokens(slug),
			}
			isLCC := false
			for _, c := range communeSlugs {
				if strings.Contains(slug, c) {
					isLCC = true
					break
				}
			}
			if isLCC {
				local = append(local, cand)
			} else {
				all = append(all, cand)
			}
		}
	}
	return local, all, nil
}

// searchCirkwiSitemap matche le libellé d'un circuit LCC contre les slugs des
// fiches Cirkwi indexées. Pondère par le nombre de mots-clés significatifs en
// commun, avec un bonus si un mot-clé du titre LCC apparaît comme sous-chaîne
// du slug (variantes singulier/pluriel). Seuil minimal : 2 mots-clés.
func searchCirkwiSitemap(libelle string) (id, slug string, score int, weak bool, err error) {
	title, commune := splitLibelle(libelle)
	wanted := significantTokens(title)
	if len(wanted) == 0 {
		return "", "", 0, false, fmt.Errorf("titre sans mot-clé discriminant")
	}
	communeTokens := significantTokens(commune)
	// 1. Recherche dans le sous-ensemble Liffré-Cormier (seuil ≥ 2, commune
	//    obligatoirement présente dans le slug). Match considéré fort.
	if bid, bslug, bscore := bestMatch(cirkwiSitemapCache, wanted, communeTokens, true); bscore >= 2 {
		return bid, bslug, bscore, false, nil
	}
	// 2. Repli sur l'ensemble du sitemap (sans contrainte de commune) : utile
	//    quand le slug d'une fiche officielle n'inclut pas le nom de la commune.
	//    Seuil ≥ 3, ou bien score == len(wanted) si ≥ 2 (100 % de couverture
	//    des mots-clés discriminants du titre). Match marqué FAIBLE pour
	//    permettre une validation manuelle (la commune n'est pas garantie).
	if bid, bslug, bscore := bestMatch(cirkwiSitemapAll, wanted, nil, false); bscore >= 3 || (bscore >= 2 && bscore == len(wanted)) {
		return bid, bslug, bscore, true, nil
	}
	return "", "", 0, false, fmt.Errorf("aucune correspondance suffisante dans le sitemap Cirkwi")
}

func bestMatch(cands []cirkwiCandidate, wanted, communeTokens map[string]bool, requireCommune bool) (id, slug string, score int) {
	for _, c := range cands {
		if requireCommune && !containsCommune(communeTokens, c.tokens, c.slug) {
			continue
		}
		s := 0
		for tok := range wanted {
			switch {
			case c.tokens[tok]:
				s++
			case len(tok) >= 5 && strings.Contains(c.slug, tok):
				s++
			}
		}
		// départage : à score égal, on garde le slug le plus court (titre plus
		// précis et probablement le circuit "principal" plutôt qu'une variante).
		if s > score || (s == score && id != "" && len(c.slug) < len(slug)) {
			id, slug, score = c.id, c.slug, s
		}
	}
	return id, slug, score
}

// findIVTourGPX récupère la page d'une fiche Ille-et-Vilaine Tourisme et y
// extrait le premier lien .gpx (typiquement cdt35.media.tourinsoft.eu/upload/…).
func findIVTourGPX(pageURL string) (string, error) {
	body, err := httpGetBody(pageURL)
	if err != nil {
		return "", err
	}
	m := ivTourGPXRE.Find(body)
	if m == nil {
		return "", fmt.Errorf("aucun lien GPX sur %s", pageURL)
	}
	return string(m), nil
}

// downloadGPX télécharge un GPX et vérifie qu'il s'agit bien d'un fichier XML
// GPX — Cirkwi renvoie parfois une page d'erreur HTML 200 ("Impossible de
// trouver le circuit demandé.") quand un circuit n'a pas de trace exportable.
func downloadGPX(src, dest string) error {
	body, err := httpGetBody(src)
	if err != nil {
		return err
	}
	head := strings.TrimSpace(string(body))
	if len(head) > 200 {
		head = head[:200]
	}
	if !strings.HasPrefix(head, "<?xml") {
		return fmt.Errorf("réponse non-GPX : %q", head)
	}
	return os.WriteFile(dest, body, 0644)
}

func httpGetBody(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (carto)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requête : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("statut HTTP : %s (url=%s)", resp.Status, u)
	}
	return io.ReadAll(resp.Body)
}

