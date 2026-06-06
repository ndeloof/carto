package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

// ID stable de la ressource GeoJSON PDIPR 35 sur le portail open data CKAN
// d'Ille-et-Vilaine. L'URL de téléchargement réelle change à chaque mise à jour
// (elle inclut une date), on la résout donc via l'API CKAN.
const pdiprResourceID = "bc24c58d-7ac8-402c-9c63-a6b99e758e67"
const ckanResourceShowURL = "https://data.ille-et-vilaine.fr/api/3/action/resource_show?id=" + pdiprResourceID

var communesLiffreCormier = map[string]bool{
	"Chasné-sur-Illet":       true,
	"Dourdain":               true,
	"Ercé-près-Liffré":       true,
	"Gosné":                  true,
	"La Bouëxière":           true,
	"Liffré":                 true,
	"Livré-sur-Changeon":     true,
	"Mézières-sur-Couesnon":  true,
	"Saint-Aubin-du-Cormier": true,
}

var pdiprOutput string

func init() {
	pdiprCmd.Flags().StringVarP(&pdiprOutput, "output", "o", "pdipr_liffre_cormier.geojson", "fichier GeoJSON de sortie")
	rootCmd.AddCommand(pdiprCmd)
}

var pdiprCmd = &cobra.Command{
	Use:   "pdipr",
	Short: "Télécharge le PDIPR 35 et filtre les communes de Liffré-Cormier",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPDIPR(pdiprOutput)
	},
}

type ckanResourceShowResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Result  struct {
		URL string `json:"url"`
	} `json:"result"`
}

type featureCollection struct {
	Type     string            `json:"type"`
	Name     string            `json:"name,omitempty"`
	CRS      json.RawMessage   `json:"crs,omitempty"`
	Features []json.RawMessage `json:"features"`
}

type feature struct {
	Properties struct {
		NomCom string `json:"NOM_COM"`
	} `json:"properties"`
}

func resolvePDIPRDownloadURL() (string, error) {
	resp, err := http.Get(ckanResourceShowURL)
	if err != nil {
		return "", fmt.Errorf("appel API CKAN : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API CKAN, statut %s", resp.Status)
	}
	var r ckanResourceShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("décodage réponse CKAN : %w", err)
	}
	if !r.Success {
		return "", fmt.Errorf("API CKAN : success=false (%s)", r.Error)
	}
	if r.Result.URL == "" {
		return "", fmt.Errorf("API CKAN : URL vide")
	}
	return r.Result.URL, nil
}

func runPDIPR(output string) error {
	fmt.Fprintln(os.Stderr, "Résolution de l'URL de téléchargement via l'API CKAN...")
	downloadURL, err := resolvePDIPRDownloadURL()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "URL :", downloadURL)

	fmt.Fprintln(os.Stderr, "Téléchargement du fichier PDIPR...")
	resp, err := http.Get(downloadURL)
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

	var fc featureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return fmt.Errorf("décodage GeoJSON : %w", err)
	}
	fmt.Fprintf(os.Stderr, "Features totales : %d\n", len(fc.Features))

	filtered := make([]json.RawMessage, 0, len(fc.Features))
	for _, raw := range fc.Features {
		var f feature
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if communesLiffreCormier[f.Properties.NomCom] {
			filtered = append(filtered, raw)
		}
	}
	fc.Features = filtered
	fmt.Fprintf(os.Stderr, "Features retenues : %d\n", len(fc.Features))

	out, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("création fichier : %w", err)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fc); err != nil {
		return fmt.Errorf("encodage : %w", err)
	}
	fmt.Fprintf(os.Stderr, "Écrit dans %s\n", output)
	return nil
}
