package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spf13/cobra"
)

const bikersCircuitsURL = "http://www.lesbikersdelaforet.fr/pages/les-bikers-de-la-foret/nos-circuits.html"

var vttOutputDir string

func init() {
	vttCmd.Flags().StringVarP(&vttOutputDir, "output", "o", "gpx", "répertoire de destination des fichiers GPX")
	rootCmd.AddCommand(vttCmd)
}

var vttCmd = &cobra.Command{
	Use:   "vtt",
	Short: "Télécharge les traces GPX des circuits VTT des Bikers de la Forêt",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVTT(vttOutputDir)
	},
}

// gpxLinkRE capture les hrefs pointant vers un fichier .gpx dans la page.
var gpxLinkRE = regexp.MustCompile(`href="([^"]+\.gpx)"`)

func runVTT(outputDir string) error {
	fmt.Fprintln(os.Stderr, "Récupération de la page des circuits :", bikersCircuitsURL)
	resp, err := http.Get(bikersCircuitsURL)
	if err != nil {
		return fmt.Errorf("récupération page : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("page, statut HTTP : %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("lecture page : %w", err)
	}

	base, err := url.Parse(bikersCircuitsURL)
	if err != nil {
		return fmt.Errorf("URL de base invalide : %w", err)
	}

	seen := map[string]bool{}
	var urls []string
	for _, m := range gpxLinkRE.FindAllSubmatch(body, -1) {
		ref, err := url.Parse(string(m[1]))
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref).String()
		if seen[abs] {
			continue
		}
		seen[abs] = true
		urls = append(urls, abs)
	}
	sort.Strings(urls)
	if len(urls) == 0 {
		return fmt.Errorf("aucun lien GPX trouvé sur %s", bikersCircuitsURL)
	}
	fmt.Fprintf(os.Stderr, "GPX référencés : %d\n", len(urls))

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("création répertoire : %w", err)
	}

	for _, u := range urls {
		name := path.Base(u)
		dest := filepath.Join(outputDir, name)
		if err := downloadFile(u, dest); err != nil {
			fmt.Fprintf(os.Stderr, "  %s : ERREUR %v\n", name, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %s\n", name)
	}
	fmt.Fprintf(os.Stderr, "Téléchargements terminés dans %s\n", outputDir)
	return nil
}

func downloadFile(src, dest string) error {
	resp, err := http.Get(src)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("statut %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}
