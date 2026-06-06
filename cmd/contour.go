package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

// Code SIREN de la communauté de communes Liffré-Cormier Communauté.
// L'API geo.api.gouv.fr renvoie le contour de l'EPCI au format GeoJSON.
const liffreCormierEPCICode = "243500774"
const epciContourURL = "https://geo.api.gouv.fr/epcis/" + liffreCormierEPCICode + "?format=geojson&geometry=contour"

var contourOutput string

func init() {
	contourCmd.Flags().StringVarP(&contourOutput, "output", "o", "liffre_cormier_contour.geojson", "fichier GeoJSON de sortie")
	rootCmd.AddCommand(contourCmd)
}

var contourCmd = &cobra.Command{
	Use:   "contour",
	Short: "Récupère le contour de Liffré-Cormier Communauté",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runContour(contourOutput)
	},
}

func runContour(output string) error {
	fmt.Fprintln(os.Stderr, "Téléchargement du contour EPCI :", epciContourURL)
	resp, err := http.Get(epciContourURL)
	if err != nil {
		return fmt.Errorf("téléchargement : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("téléchargement, statut HTTP : %s", resp.Status)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("lecture : %w", err)
	}

	// On revalide en JSON pour s'assurer que la réponse est bien formée,
	// puis on réécrit en indenté pour la lisibilité.
	var any json.RawMessage
	if err := json.Unmarshal(raw, &any); err != nil {
		return fmt.Errorf("réponse non JSON : %w", err)
	}
	var pretty []byte
	if pretty, err = json.MarshalIndent(any, "", "  "); err != nil {
		return fmt.Errorf("ré-encodage : %w", err)
	}

	if err := os.WriteFile(output, pretty, 0644); err != nil {
		return fmt.Errorf("écriture fichier : %w", err)
	}
	fmt.Fprintf(os.Stderr, "Écrit dans %s\n", output)
	return nil
}
