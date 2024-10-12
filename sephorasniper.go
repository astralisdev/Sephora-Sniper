package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/texttheater/golang-levenshtein/levenshtein"
)

const storeIDFile = "store_ids"
const intervalFile = "check_intervaltimer.txt"
const redColor = "\033[31m"
const resetColor = "\033[0m"

const endpoint_url_fr = "https://www.sephora.fr/on/demandware.store/Sites-Sephora_FR-Site/fr_FR/Stores-FindNearestStores?pid=735577&clickcollect=true&pdpstock=true&latitude=38.2088210000000&longitude=15.5470420606796&searchedRadius=150000&storeservices="
const endpoint_url_it = "https://www.sephora.it/on/demandware.store/Sites-Sephora_IT-Site/it_IT/Stores-FindNearestStores?pid=735577&clickcollect=true&pdpstock=true&latitude=38.2088210000000&longitude=15.5470420606796&searchedRadius=15000&storeservices="
const endpoint_url_de = "https://www.sephora.de/on/demandware.store/Sites-Sephora_DE-Site/de_DE/Stores-FindNearestStores?pid=735577&clickcollect=true&pdpstock=true&latitude=38.2088210000000&longitude=15.5470420606796&searchedRadius=150000&storeservices="

var storeResponse StoreResponse

type WorkingStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Schedule struct {
	Day  string `json:"Day"`
	Time string `json:"Time"`
}

type StoreService struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Customised Type for scheduleForJsonLD
type ScheduleForJsonLD []string

// Override for manipulate a json field that could be a single string than also an array of strings
func (s *ScheduleForJsonLD) UnmarshalJSON(data []byte) error {
	var singleString string
	if err := json.Unmarshal(data, &singleString); err == nil {
		*s = ScheduleForJsonLD{singleString} // se è una stringa, mettiamo in un array
		return nil
	}

	var arrayOfStrings []string
	if err := json.Unmarshal(data, &arrayOfStrings); err != nil {
		return err
	}
	*s = arrayOfStrings
	return nil
}

type Location struct {
	ID                     string            `json:"id"`
	OMSID                  string            `json:"omsId"`
	Name                   string            `json:"name"`
	City                   string            `json:"city"`
	URL                    string            `json:"url"`
	Country                string            `json:"country"`
	CountryCode            string            `json:"country_code"`
	Postal                 string            `json:"postal"`
	Address1               string            `json:"address1"`
	Address2               string            `json:"address2"`
	Address3               string            `json:"address3"`
	Phone                  string            `json:"phone"`
	WorkingStatus          WorkingStatus     `json:"working_status"`
	Latitude               float64           `json:"latitude"`
	Longitude              float64           `json:"longitude"`
	Favorite               bool              `json:"favorite"`
	Schedule               []Schedule        `json:"schedule"`
	ScheduleForJsonLD      ScheduleForJsonLD `json:"scheduleForJsonLD"` // Modificato in tipo personalizzato
	Image                  string            `json:"image"`
	Distance               float64           `json:"distance"`
	StoreServices          []StoreService    `json:"store_services"`
	Exceptional            *string           `json:"exceptional"` // Usare *string per permettere il valore null
	ExceptionalOpeningText string            `json:"exceptionalOpeningText"`
	ExceptionalClosingText string            `json:"exceptionalClosingText"`
	HasBookable            bool              `json:"has_bookable"`
	AttentionMessage       string            `json:"attention_message"`
	Activation             bool              `json:"activation"`
	BookingAPIKey          string            `json:"bookingAPIKey"`
	EnableDeliveryToStore  bool              `json:"enableDeliveryToStore"`
	EnableClickCollect     bool              `json:"enableClickCollect"`
	ProductAvailability    bool              `json:"product_availability"` // Assicurati che questo campo esista nel JSON
}

type StoreResponse struct {
	Success           bool       `json:"success"`
	Radius            int        `json:"radius"`
	FavStoreId        *string    `json:"favStoreId"`
	Locations         []Location `json:"locations"`
	Timestamp         string     `json:"timestamp"`
	IsClickAndCollect bool       `json:"isClickAndCollect"`
}

type DiscordWebhookPayload struct {
	Content string `json:"content"`
}

func sendDiscordNotification(webhookURL string, message string) error {
	payload := DiscordWebhookPayload{
		Content: message,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON payload: %v", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("received non-204 response status: %d", resp.StatusCode)
	}

	return nil
}

func downloadStoreData(endpoint_url string) {
	customTransport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).DialContext,
	}

	client := &http.Client{
		Transport: customTransport,
		Timeout:   15 * time.Second,
	}

	req, err := http.NewRequest("GET", endpoint_url, nil)
	if err != nil {
		log.Fatalf("Errore nel creare la richiesta: %v", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Errore nel fare la richiesta: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Errore: risposta HTTP %d ricevuta", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Errore nel leggere il corpo della risposta: %v", err)
	}

	err = json.Unmarshal(body, &storeResponse)
	if err != nil {
		log.Fatalf("Errore nel decodificare il JSON: %v", err)
	}
}

func checkProductAvailability(storeIDs []string, endpoint_url string, webhookurl string) {
	// Creazione di un client HTTP personalizzato con timeout
	customTransport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second, // Timeout per la connessione
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{
		Transport: customTransport,
		Timeout:   15 * time.Second, // Timeout totale per la richiesta
	}

	// Creazione di una nuova richiesta HTTP
	req, err := http.NewRequest("GET", endpoint_url, nil)
	if err != nil {
		log.Fatalf("Errore nel creare la richiesta: %v", err)
	}

	// Aggiunta dell'header User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/92.0.4515.131 Safari/537.36")

	// Richiesta HTTP
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Errore nel fare la richiesta: %v", err)
	}
	defer resp.Body.Close()

	// Controllo dello stato HTTP
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Errore: risposta HTTP %d ricevuta", resp.StatusCode)
	}

	// Lettura del corpo della risposta
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Errore nel leggere il corpo della risposta: %v", err)
	}

	// Decodifica del JSON nella struct StoreResponse
	var storeResponse StoreResponse
	err = json.Unmarshal(body, &storeResponse)
	if err != nil {
		log.Fatalf("Errore nel decodificare il JSON: %v", err)
	}

	// Controllo della disponibilità del prodotto negli Store ID specificati
	for _, store := range storeResponse.Locations {
		for _, storeID := range storeIDs {
			if store.ID == storeID {
				if store.ProductAvailability {
					// Usa il colore verde se disponibile
					color.Green("Store ID: %s, Name and Address: %s %s, Availability: %t\n", store.ID, store.Name, store.Address1, store.ProductAvailability)

					message := fmt.Sprintf("**🛍️ SEPHORA SNIPER 🏪** \n 🛒 The Product is available in the store **%s**! \nStore Address: %s", store.Name, store.Address1)
					err := sendDiscordNotification(webhookurl, message)
					if err != nil {
						fmt.Printf("Errore nell'invio del messaggio su Discord: %v\n", err)
					}

				} else {
					// Altrimenti stampa in giallo
					color.Yellow("Store ID: %s, Name and Address: %s %s, Availability: %t\n", store.ID, store.Name, store.Address1, store.ProductAvailability)
				}
				break
			}
		}
	}
}

// Funzione per leggere gli ID dei negozi dal file
func readStoreIDs() ([]string, error) {
	file, err := os.Open(storeIDFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var storeIDs []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		storeIDs = append(storeIDs, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return storeIDs, nil
}

// Funzione per scrivere gli ID dei negozi nel file
func writeStoreID(id string) error {
	file, err := os.OpenFile(storeIDFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.WriteString(id + "\n"); err != nil {
		return err
	}

	return nil
}

// Funzione per leggere l'intervallo dal file
func readCheckInterval() (time.Duration, error) {
	file, err := os.Open(intervalFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // Se il file non esiste, ritorna 0
		}
		return 0, err
	}
	defer file.Close()

	var hours int
	_, err = fmt.Fscan(file, &hours)
	if err != nil {
		return 0, err
	}
	return time.Duration(hours) * time.Hour, nil
}

// Funzione per scrivere l'intervallo nel file
func writeCheckInterval(interval time.Duration) error {
	file, err := os.Create(intervalFile)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "%d", int(interval.Hours()))
	return err
}

func getStoreIDsByCity(cityName string, endpoint_url string) {
	var storesFound bool

	// Scarichiamo i dati degli store usando la funzione esistente
	downloadStoreData(endpoint_url)

	// Verifica se ci sono negozi disponibili nella risposta
	if len(storeResponse.Locations) == 0 {
		fmt.Println("Nessun negozio trovato nella risposta.")
		return
	}

	// Convertiamo l'input dell'utente in lowercase per un confronto case-insensitive
	lowerCityName := strings.ToLower(cityName)

	// Iteriamo su tutti gli store disponibili
	for _, store := range storeResponse.Locations {
		// Confrontiamo i nomi delle città convertendoli in lowercase
		if strings.ToLower(store.City) == lowerCityName {
			// Stampa sia lo StoreID che l'indirizzo (Address1)
			color.Cyan("Store ID: %s, Address: %s\n", store.ID, store.Address1)
			storesFound = true
		}
	}

	// Se non sono stati trovati store nella città indicata
	if !storesFound {
		color.Red("No stores found in the city: %s\n", cityName)
		fmt.Println("Please check your input.")

		// Suggerisci città simili
		similarCities := suggestSimilarCities(cityName)
		if len(similarCities) > 0 {
			fmt.Println("Did you mean one of these cities?")
			for _, suggestion := range similarCities {
				fmt.Println(suggestion)
			}
		} else {
			fmt.Println("No similar cities found.")
		}
	}
}

// Funzione per suggerire città simili in caso di mancata corrispondenza esatta
func suggestSimilarCities(inputCity string) []string {
	var suggestions []string

	// Convertiamo l'input in lowercase per confronto case-insensitive
	lowerInputCity := strings.ToLower(inputCity)

	// Mappa per tenere traccia delle distanze delle città trovate
	cityDistances := make(map[string]int)

	// Iteriamo su tutti gli store disponibili per calcolare le distanze
	for _, store := range storeResponse.Locations {
		// Convertiamo il nome della città in lowercase per il confronto
		lowerCityName := strings.ToLower(store.City)

		// Calcoliamo la distanza di Levenshtein tra l'input e il nome della città
		distance := levenshtein.DistanceForStrings([]rune(lowerInputCity), []rune(lowerCityName), levenshtein.DefaultOptions)

		// Salviamo la distanza nella mappa
		cityDistances[store.City] = distance
	}

	// Troviamo le 2-3 città con la distanza più bassa
	closestCities := findTopMatches(cityDistances, 3)

	// Aggiungiamo le città trovate ai suggerimenti
	suggestions = append(suggestions, closestCities...)

	// Ritorniamo la lista delle città suggerite
	return suggestions
}

// Funzione di supporto per trovare le città con la distanza più bassa
func findTopMatches(cityDistances map[string]int, maxMatches int) []string {
	type cityDistance struct {
		City     string
		Distance int
	}

	// Convertiamo la mappa in un array di struct per ordinare le distanze
	var sortedCities []cityDistance
	for city, distance := range cityDistances {
		sortedCities = append(sortedCities, cityDistance{City: city, Distance: distance})
	}

	// Ordiniamo l'array per distanza crescente
	sort.Slice(sortedCities, func(i, j int) bool {
		return sortedCities[i].Distance < sortedCities[j].Distance
	})

	// Prendiamo i primi `maxMatches` risultati
	var topMatches []string
	for i := 0; i < maxMatches && i < len(sortedCities); i++ {
		topMatches = append(topMatches, sortedCities[i].City)
	}

	return topMatches
}

// Funzione per salvare la selezione del paese su un file
func writeCountrySelection(country string) error {
	return os.WriteFile("country_selection.txt", []byte(country), 0644)
}

// Funzione per leggere la selezione del paese dal file
func readCountrySelection() (string, error) {
	content, err := os.ReadFile("country_selection.txt")
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func getWebHookUrl() string {
	fmt.Println("Please enter your Discord webhook URL:")
	var webhookURL string
	fmt.Scan(&webhookURL)

	// Salva l'URL nel file
	err := writeWebhookURL(webhookURL)
	if err != nil {
		log.Fatalf("Error saving webhook URL: %v", err)
	}

	color.Green("Webhook URL saved successfully!")
	return webhookURL
}

// Funzione per scrivere l'URL del webhook nel file
func writeWebhookURL(url string) error {
	return os.WriteFile("webhook_url.txt", []byte(url), 0644)
}

func readWebhookURL() (string, error) {
	content, err := os.ReadFile("webhook_url.txt")
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func main() {

	// Ciclo continuo fino a quando l'utente non sceglie di avviare il programma (opzione 4)
	for {

		// Scelta del paese salvata in un file
		country, err := readCountrySelection()
		if err != nil {
			fmt.Println("Please select your country (IT, DE, FR):")
			var selectedCountry string
			for {
				fmt.Scan(&selectedCountry)
				selectedCountry = strings.ToUpper(selectedCountry)
				if selectedCountry == "IT" || selectedCountry == "DE" || selectedCountry == "FR" {
					err := writeCountrySelection(selectedCountry)
					if err != nil {
						log.Fatalf("Errore nella scrittura della selezione del paese: %v", err)
					}
					country = selectedCountry
					break
				} else {
					fmt.Println("Invalid selection. Please select either IT, DE, or FR.")
				}
			}
		} else {
			fmt.Printf("Country selected: %s\n", country)
		}

		// Si definisce l'url corretto in base alla scelta
		var choosen_region_url string
		if country == "IT" {
			choosen_region_url = endpoint_url_it
		} else if country == "DE" {
			choosen_region_url = endpoint_url_de
		} else {
			choosen_region_url = endpoint_url_fr
		}


		hookurl, error := readWebhookURL()
		var hook_status bool
		if error != nil {
			color.Red("Error reading webhook url, please check the file.")
		}
		if len(hookurl) > 1 {
			hook_status = true
		} else {
			hook_status = false
		}


		fmt.Println(" __            _                       __       _                 ")
		fmt.Println("/ _\\ ___ _ __ | |__   ___  _ __ __ _  / _\\_ __ (_)_ __   ___ _ __ ")
		fmt.Println("\\ \\ / _ \\ '_ \\| '_ \\ / _ \\| '__/ _` | \\ \\| '_ \\| | '_ \\ / _ \\ '__|")
		fmt.Println("_\\ \\  __/ |_) | | | | (_) | | | (_| | _\\ \\ | | | | |_) |  __/ |   ")
		fmt.Println("\\__/\\___| .__/|_| |_|\\___/|_|  \\__,_| \\__/_| |_|_| .__/ \\___|_|   ")
		fmt.Println("        |_|                                      |_|              ")
		fmt.Print("#2024 rickyita© technologies ")
		fmt.Println()

		var storeIDs []string
		storeIDs, err = readStoreIDs()
		if err != nil {
			log.Fatalf("Errore nella lettura degli ID dei negozi: %v", err)
		}

		// Lettura del tempo di intervallo
		checkInterval, err := readCheckInterval()
		if err != nil {
			log.Fatal("Errore nella lettura dell'intervallo di controllo: %v", err)
		}

		// Stampa gli store ID attuali e il tempo di intervallo
		fmt.Println("Current monitored Store List: ")
		for _, id := range storeIDs {
			fmt.Println(id)
		}

		fmt.Println("-----------------------")
		fmt.Printf("Current Interval Delay: %v\n", checkInterval)
		fmt.Println("+-+-+-+-+-+-+-+-+-+-+-+")
		fmt.Println()

		// Menu di selezione
		fmt.Println("Please enter an option: ")
		fmt.Println("1) Add StoreID")
		fmt.Println("2) Set Interval for Availability Checks ")
		fmt.Println("3) City StoreIDs Lookup")
		fmt.Println("4) Start Sniper")

		fmt.Println()
		fmt.Print("5) Change Country - ")
		fmt.Print("Country Selected: ")
		color.Green("%s", country)
		fmt.Print("")
		fmt.Print("6) Add WebHook Url - ")
		if !(hook_status) {
			color.Red("Not Added yet ❌")
		} else {
			color.Green("Added Already ✅")
		}
		fmt.Println("------------------------")
		fmt.Println()

		var user_input int
		fmt.Scan(&user_input)

		switch user_input {
		case 1:
			// Aggiunta di Store ID
			for {
				fmt.Println("Do you want to add a new StoreID? Please enter y or n")
				var choice string
				fmt.Scan(&choice)
				if choice == "y" {
					fmt.Println("Enter the new StoreID (format ITCODE): then press send")
					var newID string
					fmt.Scan(&newID)

					err := writeStoreID(newID)
					if err != nil {
						log.Fatalf("Errore nella scrittura dell'ID del negozio: %v", err)
					}
					fmt.Println("StoreID added successfully!")

					storeIDs = append(storeIDs, newID)
					fmt.Println("Current List: ", storeIDs)
				} else {
					break
				}
			}

		case 2:
			// Impostazione dell'intervallo di controllo
			fmt.Println("Set the check interval (in hours):")
			var hours int
			fmt.Scan(&hours)
			checkInterval = time.Duration(hours) * time.Hour
			if err := writeCheckInterval(checkInterval); err != nil {
				log.Fatalf("Errore nella scrittura dell'intervallo di controllo: %v", err)
			}
			fmt.Printf("Check interval set to %d hours.\n", hours)

		case 3:
			// Ricerca degli Store ID per città
			fmt.Println("Please write the name of the City:  (Example: Milano/Paris/Berlin)")
			var cityName string
			fmt.Scanln(&cityName)

			// Endpoint returns all cities name in UPPER format and it's very sensitive, so user input is safe.
			upper := strings.ToUpper(cityName)

			color.Magenta("Stores found for %s: ", cityName)
			getStoreIDsByCity(upper, choosen_region_url)

			fmt.Println()

		case 4:
			if len(storeIDs) == 0 {
				log.Fatal("Error: Store ID List is empty")
			} else {
				fmt.Println()
				fmt.Println("Starting sniper...")
				fmt.Println()
				for {
					checkProductAvailability(storeIDs, choosen_region_url, hookurl)
					//Timestamp
					timestamp := time.Now().Format("2006-01-02 15:04:05")
					fmt.Printf("Checked at: %s\n", timestamp)
					fmt.Println()

					// Inizializza il timer per l'output
					for remaining := checkInterval; remaining > 0; remaining -= time.Second {
						fmt.Printf("\r"+redColor+"Leave this Terminal Page open, next check will be in %v seconds"+resetColor, int(remaining.Seconds()))
						time.Sleep(time.Second)
					}
					fmt.Println()

				}
			}

		case 5:
			fmt.Println("Do you want to change the region? Please enter y or n")
			var user_input string
			fmt.Scan(&user_input)

			if user_input == "y" {
				fmt.Println("Please enter the new region (e.g., IT, FR, DE):")
				fmt.Println()

				var newRegion string
				fmt.Scan(&newRegion)
				// Scrive la nuova regione nel file
				if err := writeCountrySelection(newRegion); err != nil {
					log.Fatalf("Errore nella scrittura della selezione della regione: %v", err)
				}

				color.Green("Region changed to %s.\n", newRegion)

			}

		case 6:
			getWebHookUrl()

		default:
			fmt.Println("Invalid option. Please check your input and try again.")
		}
		time.Sleep(4 * time.Second)

		fmt.Println()
		fmt.Println()
	}
}
