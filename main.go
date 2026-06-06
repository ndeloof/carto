package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// ID stable de la ressource GeoJSON PDIPR 35 sur le portail open data CKAN
// d'Ille-et-Vilaine. L'URL de téléchargement réelle change à chaque mise à jour
// (elle inclut une date), on la résout donc via l'API CKAN.
const resourceID = "bc24c58d-7ac8-402c-9c63-a6b99e758e67"
const ckanResourceShowURL = "https://data.ille-et-vilaine.fr/api/3/action/resource_show?id=" + resourceID

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

type ckanResourceShowResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Result  struct {
		URL string `json:"url"`
	} `json:"result"`
}

type FeatureCollection struct {
	Type     string            `json:"type"`
	Name     string            `json:"name,omitempty"`
	CRS      json.RawMessage   `json:"crs,omitempty"`
	Features []json.RawMessage `json:"features"`
}

type Feature struct {
	Properties struct {
		NomCom string `json:"NOM_COM"`
	} `json:"properties"`
}

func resolveDownloadURL() (string, error) {
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

func main() {
	output := "pdipr_liffre_cormier.geojson"
	if len(os.Args) > 1 {
		output = os.Args[1]
	}

	fmt.Fprintln(os.Stderr, "Résolution de l'URL de téléchargement via l'API CKAN...")
	downloadURL, err := resolveDownloadURL()
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "URL :", downloadURL)

	fmt.Fprintln(os.Stderr, "Téléchargement du fichier PDIPR...")
	resp, err := http.Get(downloadURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur HTTP :", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "statut HTTP inattendu : %s\n", resp.Status)
		os.Exit(1)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur lecture :", err)
		os.Exit(1)
	}

	var fc FeatureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		fmt.Fprintln(os.Stderr, "erreur JSON :", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Features totales : %d\n", len(fc.Features))

	filtered := make([]json.RawMessage, 0, len(fc.Features))
	for _, raw := range fc.Features {
		var f Feature
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
		fmt.Fprintln(os.Stderr, "erreur création fichier :", err)
		os.Exit(1)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fc); err != nil {
		fmt.Fprintln(os.Stderr, "erreur encodage :", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Écrit dans %s\n", output)
}
