package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type ConfigFile struct {
	TMDbToken           string          `json:"tmdb_token"`
	DownloadPathPrefix  string          `json:"download_path_prefix"`
	RemoteDriveName     string          `json:"remote_drvie_name"`
	RemoteDefaultPath   string          `json:"remote_default_path"`
	RclonePath          string          `json:"rclone_path"`
	RcloneConfig        string          `json:"rclone_config"`
	AVDataCapture       string          `json:"av_data_capture"`
	AVDataCaptureConfig string          `json:"av_data_capture_config"`
	Library             []LibraryInfo   `json:library`
	AutoScan            AutoScanSetting `json:"auto_scan"`
	libraryList         map[string]int
}

type LibraryInfo struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	PlexLibraryID int    `json:"plex_library_id"`
	IsAv          bool   `json:"is_av"`
	IsMovie       bool   `json:"is_movie"`
}

type AutoScanSetting struct {
	Enable         bool   `json:"enable"`
	PlexServerPath string `json:"plex_server_path"`
	PlexScanPrefix string `json:"plex_scan_perfix"`
	PlexToken      string `json:"plex_token"`
}

var Config ConfigFile

func main() {
	//flags
	configPath := flag.String("config", "config.json", "custom config path")
	fileNumber := flag.Int("file-number", 0, "the number of downloaded files")
	filePath := flag.String("file-path", "/", "the path of downloaded path")
	flag.Parse()
	if *fileNumber < 1 || *filePath == "/" {
		log.Fatal("Error: file number or file path is invalid")
	}

	//init config file
	configFile, err := os.Open(*configPath)
	if err != nil {
		log.Fatal("Error: failed to open config file")
	}
	defer configFile.Close()
	byteValue, _ := ioutil.ReadAll(configFile)
	json.Unmarshal([]byte(byteValue), &Config)
	Config.libraryList = make(map[string]int)
	for k, v := range Config.Library {
		Config.libraryList[v.Name] = k + 1
	}

	log.Printf("file number: %v, task path: %v", *fileNumber, *filePath)
	//check filePath input
	match, _ := regexp.MatchString(fmt.Sprintf("%v/.*", Config.DownloadPathPrefix), *filePath)
	if !match {
		log.Fatal("Error: file path is illegal")
	}
	localPath, remotePath, plexLibID := parsePath(*filePath, *fileNumber)
	upload(localPath, remotePath)
	if Config.AutoScan.Enable {
		time.Sleep(60 * time.Second)
		scan(remotePath, plexLibID)
	}
}

func parsePath(filePath string, fileNumber int) (string, string, int) {
	localPath := filePath
	remotePath := fmt.Sprintf("/%v", Config.RemoteDefaultPath)
	plexLibID := 0
	match, _ := regexp.MatchString(fmt.Sprintf("%v/auto", Config.DownloadPathPrefix), localPath)
	if match {
		typeRegexp := regexp.MustCompile(fmt.Sprintf("%v/auto/([^/]*)", Config.DownloadPathPrefix))
		params := typeRegexp.FindStringSubmatch(localPath)
		if Config.libraryList[params[1]] != 0 {
			localPath, remotePath, plexLibID = parseLibrary(localPath, &Config.Library[Config.libraryList[params[1]]-1])
		} else {
			log.Error("Unknow library")
			if fileNumber != 1 {
				fileNameRegexp := regexp.MustCompile(fmt.Sprintf("%v/([^/]*)", Config.DownloadPathPrefix))
				params := fileNameRegexp.FindStringSubmatch(localPath)
				localPath = fmt.Sprintf("%v/%v", Config.DownloadPathPrefix, params[1])
				remotePath = fmt.Sprintf("%v/%v", remotePath, params[1])
			}
		}

	} else {
		if fileNumber != 1 {
			fileNameRegexp := regexp.MustCompile(fmt.Sprintf("%v/([^/]*)", Config.DownloadPathPrefix))
			params := fileNameRegexp.FindStringSubmatch(localPath)
			localPath = fmt.Sprintf("%v/%v", Config.DownloadPathPrefix, params[1])
			remotePath = fmt.Sprintf("%v/%v", remotePath, params[1])
		}
	}
	log.Info("localPath: ", localPath, " remotePath: ", remotePath, " plexLibID: ", plexLibID)
	return localPath, remotePath, plexLibID
}

func upload(localPath, remotePath string) {
	cmd := exec.Command("/bin/bash", "-c", fmt.Sprintf("%v --config=\"%v\" copy \"%v\" \"%v:%v\"", Config.RclonePath, Config.RcloneConfig, localPath, Config.RemoteDriveName, remotePath))
	counter := 0
	for ; counter < 3; counter++ {
		err := cmd.Run()
		if err != nil {
			log.Error("Upload failed: ", err)
		} else {
			log.Println("Upload success: ", localPath)
			cleanUp(localPath)
			break
		}
	}
}

func capture(filePath string) {
	os.Chdir(Config.AVDataCaptureConfig)
	cmd := exec.Command("/bin/bash", "-c", fmt.Sprintf("%v -p \"%v\"", Config.AVDataCapture, filePath))
	err := cmd.Run()
	var (
		ee *exec.ExitError
		pe *os.PathError
	)
	if errors.As(err, &ee) {
		log.Printf("exit code error: %v", ee.ExitCode())
	} else if errors.As(err, &pe) {
		log.Printf("os.PathError: %v", pe)
	} else {
		log.Printf("Av_data_capture scan success! %v", filePath)
	}
}

func scan(remotePath string, plexLibID int) {
	if plexLibID < 1 {
		log.Error("Plex Library ID is illegal")
		return
	}
	params := url.Values{}
	URL, err := url.Parse(fmt.Sprintf("%v/library/sections/%v/refresh", Config.AutoScan.PlexServerPath, plexLibID))
	if err != nil {
		return
	}
	params.Set("path", Config.AutoScan.PlexScanPrefix+remotePath)
	params.Set("X-Plex-Token", Config.AutoScan.PlexToken)
	URL.RawQuery = params.Encode()
	urlPath := URL.String()
	resp, err := http.Get(urlPath)
	if err != nil {
		log.Error(err)
	}
	if resp.StatusCode == 200 {
		log.Info("Auto scanning: %v", remotePath)
	} else {
		log.Warn("Failed to scan: %v", remotePath)
	}
}

func cleanUp(localPath string) {
	os.RemoveAll(localPath)
	localPath = localPath[:strings.LastIndex(localPath, "/")]
	for len(localPath) > len(Config.DownloadPathPrefix) && strings.LastIndex(localPath, "/") > 0 {
		os.Remove(localPath)
		localPath = localPath[:strings.LastIndex(localPath, "/")]
	}
}

func parseLibrary(localPath string, libInfo *LibraryInfo) (string, string, int) {
	remotePath := Config.RemoteDefaultPath
	if libInfo.IsAv {
		localPath, remotePath = parseXXX(localPath, libInfo.Name, libInfo.Path)
	} else if libInfo.IsMovie {
		localPath, remotePath = parseMovie(localPath, libInfo.Name, libInfo.Path)
	} else {
		localPath, remotePath = parseTV(localPath, libInfo.Name, libInfo.Path)
	}
	return localPath, remotePath, libInfo.PlexLibraryID
}

func parseTV(filePath string, name string, path string) (string, string) {
	infoRegexp := regexp.MustCompile(fmt.Sprintf("%v/auto/%v/([^/]*)/([^/]*)", Config.DownloadPathPrefix, name))
	params := infoRegexp.FindStringSubmatch(filePath)
	localPath := fmt.Sprintf("%v/auto/%v/%v/%v", Config.DownloadPathPrefix, name, params[1], params[2])
	episodeRegexp := regexp.MustCompile("S(\\d+)E(\\d+)")
	episodeParams := episodeRegexp.FindStringSubmatch(params[2])
	season, err := strconv.Atoi(episodeParams[1])
	if err != nil {
		log.Error("Cannot get season number")
	}
	episode, err := strconv.Atoi(episodeParams[2])
	if err != nil {
		log.Error("Cannot get episode number")
	}
	title, year := getTMDbTVTitle(params[1])
	remotePath := fmt.Sprintf("%v/%v (%v)/S%02d/", path, title, year, season)
	err = filepath.Walk(localPath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				os.Rename(path, fmt.Sprintf("%vS%02dE%02d.%v", path[:len(path)-len(info.Name())], season, episode, info.Name()))
			}
			return nil
		})
	if err != nil {
		log.Error("TV rename ERROR: ", err)
	}
	return localPath, remotePath
}

func parseXXX(filePath string, name string, path string) (string, string) {
	infoRegexp := regexp.MustCompile(fmt.Sprintf("%v/auto/%v/([^/]*)", Config.DownloadPathPrefix, name))
	params := infoRegexp.FindStringSubmatch(filePath)
	number := params[1]
	localPath := fmt.Sprintf("%v/auto/%v/%v", Config.DownloadPathPrefix, name, number)
	remotePath := fmt.Sprintf("%v/%v", path, number)
	counter := 1
	err := filepath.Walk(localPath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				if info.Size()/1024/1024 < 100 {
					os.Remove(path)
				} else {
					fPath := path[:strings.LastIndex(path, "/")+1]
					fileSuffix := path[strings.LastIndex(path, "."):]
					if _, err := os.Stat(fPath + number + fileSuffix); err != nil {
						if os.IsNotExist(err) {
							os.Rename(path, fPath+number+fileSuffix)
						} else {
							os.Rename(path, fPath+number+"-"+strconv.Itoa(counter)+fileSuffix)
							counter++
						}
					}
				}
			}
			return nil
		})
	if err != nil {

	}
	capture(localPath)
	return localPath, remotePath
}

func parseMovie(filePath string, name string, path string) (string, string) {
	infoRegexp := regexp.MustCompile(fmt.Sprintf("%v/auto/%v/([^/]*)", Config.DownloadPathPrefix, name))
	params := infoRegexp.FindStringSubmatch(filePath)
	localPath := fmt.Sprintf("%v/auto/%v/%v", Config.DownloadPathPrefix, name, params[1])
	title, year := getTMDbMovieTitle(params[1])
	remotePath := fmt.Sprintf("%v/%v (%v)/", path, title, year)
	return localPath, remotePath
}

func getTMDbTVTitle(tmdbID string) (string, string) {
	resp, err := http.Get(fmt.Sprintf("https://api.themoviedb.org/3/tv/%v?api_key=%v&language=en-US", tmdbID, Config.TMDbToken))
	if err != nil {
		log.Fatal("Cannot get info from TMDb")
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	var info map[string]string
	json.Unmarshal([]byte(body), &info)
	title := info["name"]
	year := info["first_air_date"][:4]
	return title, year
}

func getTMDbMovieTitle(tmdbID string) (string, string) {
	resp, err := http.Get(fmt.Sprintf("https://api.themoviedb.org/3/movie/%v?api_key=%v&language=en-US", tmdbID, Config.TMDbToken))
	if err != nil {
		log.Fatal("Cannot get info from TMDb")
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	var info map[string]string
	json.Unmarshal([]byte(body), &info)
	title := info["title"]
	year := info["release_date"][:4]
	return title, year
}
