package inventory

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/bmc-toolbox/bmcbutler/pkg/asset"
	"github.com/bmc-toolbox/bmcbutler/pkg/config"
	"github.com/bmc-toolbox/bmcbutler/pkg/metrics"
)

type Enc struct {
	Log             *logrus.Logger
	BatchSize       int
	AssetsChan      chan<- []asset.Asset
	MetricsEmitter  *metrics.Emitter
	Config          *config.Params
	FilterAssetType []string
}

type AssetAttributes struct {
	Data        map[string]Attributes `json:"data"` //map of asset IPs/Serials to attributes
	EndOfAssets bool                  `json:"end_of_assets"`
}

type Attributes struct {
	Location  string            `json:"location"`
	IpAddress []string          `json:"ipaddress"`
	Extras    *AttributesExtras `json:"extras"`
}

type AttributesExtras struct {
	State   string `json:"status"`
	Company string `json:"company"`
	//if its a chassis, this would hold serials for blades in the live state
	LiveAssets *[]string `json:"live_assets,omitempty"`
}

// Given a AttributesExtras struct,
// return all the attributes as a map
func (e *Enc) AttributesExtrasAsMap(attributeExtras *AttributesExtras) (extras map[string]string) {

	extras = make(map[string]string)

	extras["state"] = strings.ToLower(attributeExtras.State)
	extras["company"] = strings.ToLower(attributeExtras.Company)

	if attributeExtras.LiveAssets != nil {
		extras["liveAssets"] = strings.ToLower(strings.Join(*attributeExtras.LiveAssets, ","))
	} else {
		extras["liveAssets"] = ""
	}

	return extras
}

//AssetRetrieve looks at c.Config.FilterParams
//and returns the appropriate function that will retrieve assets.
func (e *Enc) AssetRetrieve() func() {

	//setup the asset types we want to retrieve data for.
	switch {
	case e.Config.FilterParams.Chassis:
		e.FilterAssetType = append(e.FilterAssetType, "chassis")
	case e.Config.FilterParams.Servers:
		e.FilterAssetType = append(e.FilterAssetType, "servers")
	case e.Config.FilterParams.Discretes:
		e.FilterAssetType = append(e.FilterAssetType, "servers")
	case !e.Config.FilterParams.Chassis && !e.Config.FilterParams.Servers:
		e.FilterAssetType = []string{"chassis", "servers"}
	}

	//Based on the filter param given, return the asset iterator method.
	switch {
	case e.Config.FilterParams.Serials != "":
		return e.AssetIterBySerial
	case e.Config.FilterParams.Ips != "":
		return e.AssetIterByIp
	default:
		return e.AssetIter
	}
}

//Executes the ENC bin file and returns the response, error
func (e *Enc) ExecCmd(args []string) (out []byte, err error) {

	//declare the executable
	executable := e.Config.InventoryParams.EncExecutable

	cmd := exec.Command(executable, args...)

	//To ignore SIGINTs recieved by bmcbutler,
	//the commands are spawned in its own process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	out, err = cmd.Output()
	if err != nil {
		return out, err
	}

	return out, err
}

func (e *Enc) encQueryBySerial(serials string) (assets []asset.Asset) {

	log := e.Log
	metric := e.MetricsEmitter
	component := "encQueryBySerial"

	//assetlookup enc --serials FOO123,BAR123
	cmdArgs := []string{"enc", "--serials", serials}

	encBin := e.Config.InventoryParams.EncExecutable
	out, err := e.ExecCmd(cmdArgs)
	if err != nil {
		log.WithFields(logrus.Fields{
			"component": component,
			"error":     err,
			"cmd":       fmt.Sprintf("%s %s", encBin, strings.Join(cmdArgs, " ")),
			"output":    fmt.Sprintf("%s", out),
		}).Fatal("Inventory query failed, lookup command returned error.")
	}

	cmdResp := AssetAttributes{}
	err = json.Unmarshal(out, &cmdResp)
	if err != nil {
		log.WithFields(logrus.Fields{
			"component": component,
			"error":     err,
			"cmd":       fmt.Sprintf("%s %s", encBin, strings.Join(cmdArgs, " ")),
			"output":    fmt.Sprintf("%s", out),
		}).Fatal("JSON Unmarshal command response returned error.")
	}

	if len(cmdResp.Data) == 0 {
		log.WithFields(logrus.Fields{
			"component": component,
			"Serial(s)": serials,
		}).Warn("No assets returned by inventory for given serial(s).")

		return []asset.Asset{}
	}

	for serial, attributes := range cmdResp.Data {
		if len(attributes.IpAddress) == 0 {
			metric.IncrCounter([]string{"inventory", "assets_noip_enc"}, 1)
			continue
		}

		extras := e.AttributesExtrasAsMap(attributes.Extras)
		assets = append(assets,
			asset.Asset{IpAddresses: attributes.IpAddress,
				Serial:   serial,
				Location: attributes.Location,
				Extra:    extras,
			})
	}

	metric.IncrCounter([]string{"inventory", "assets_fetched_enc"}, float32(len(assets)))

	return assets
}

func (e *Enc) encQueryByIp(ips string) (assets []asset.Asset) {

	log := e.Log
	metric := e.MetricsEmitter
	component := "encQueryByIp"

	// if no attributes can be recieved we return assets objs
	// populate and return slice of assets with no attributes except ips.
	populateAssetsWithNoAttributes := func() {
		ipList := strings.Split(",", ips)
		for _, ip := range ipList {
			assets = append(assets, asset.Asset{IpAddresses: []string{ip}})
		}
	}

	//assetlookup enc --serials 192.168.1.1,192.168.1.2
	cmdArgs := []string{"enc", "--ips", ips}

	encBin := e.Config.InventoryParams.EncExecutable
	out, err := e.ExecCmd(cmdArgs)
	if err != nil {
		log.WithFields(logrus.Fields{
			"component": component,
			"error":     err,
			"cmd":       fmt.Sprintf("%s %s", encBin, strings.Join(cmdArgs, " ")),
			"output":    fmt.Sprintf("%s", out),
		}).Warn("Inventory query failed, lookup command returned error.")

		populateAssetsWithNoAttributes()
		return assets
	}

	cmdResp := AssetAttributes{}
	err = json.Unmarshal(out, &cmdResp)
	if err != nil {
		log.WithFields(logrus.Fields{
			"component": component,
			"error":     err,
			"cmd":       fmt.Sprintf("%s %s", encBin, strings.Join(cmdArgs, " ")),
			"output":    fmt.Sprintf("%s", out),
		}).Fatal("JSON Unmarshal command response returned error.")
	}

	if len(cmdResp.Data) == 0 {
		log.WithFields(logrus.Fields{
			"component": component,
			"IP(s)":     ips,
		}).Debug("No assets returned by inventory for given IP(s).")

		populateAssetsWithNoAttributes()
		return assets
	}

	for serial, attributes := range cmdResp.Data {
		if len(attributes.IpAddress) == 0 {
			metric.IncrCounter([]string{"inventory", "assets_noip_enc"}, 1)
			continue
		}

		extras := e.AttributesExtrasAsMap(attributes.Extras)

		assets = append(assets,
			asset.Asset{IpAddresses: attributes.IpAddress,
				Serial:   serial,
				Location: attributes.Location,
				Extra:    extras,
			})
	}

	metric.IncrCounter([]string{"inventory", "assets_fetched_enc"}, float32(len(assets)))

	return assets
}

// encQueryByOffset returns a slice of assets and if the query reached the end of assets.
// assetType is one of 'servers/chassis'
// location is a comma delimited list of locations
func (e *Enc) encQueryByOffset(assetType string, offset int, limit int, location string) (assets []asset.Asset, endOfAssets bool) {

	component := "EncQueryByOffset"
	metric := e.MetricsEmitter
	log := e.Log

	assets = make([]asset.Asset, 0)

	var encAssetTypeFlag string

	switch assetType {
	case "servers":
		encAssetTypeFlag = "--server"
	case "chassis":
		encAssetTypeFlag = "--chassis"
	case "discretes":
		encAssetTypeFlag = "--server"
	}

	//assetlookup inventory --server --offset 0 --limit 10
	cmdArgs := []string{"inventory", encAssetTypeFlag,
		"--limit", strconv.Itoa(limit),
		"--offset", strconv.Itoa(offset)}

	//--location ams9
	if location != "" {
		cmdArgs = append(cmdArgs, "--location")
		cmdArgs = append(cmdArgs, location)
	}

	encBin := e.Config.InventoryParams.EncExecutable
	out, err := e.ExecCmd(cmdArgs)
	if err != nil {
		log.WithFields(logrus.Fields{
			"component": component,
			"error":     err,
			"cmd":       fmt.Sprintf("%s %s", encBin, strings.Join(cmdArgs, " ")),
			"output":    fmt.Sprintf("%s", out),
		}).Fatal("Inventory query failed, lookup command returned error.")
	}

	cmdResp := AssetAttributes{}
	err = json.Unmarshal(out, &cmdResp)
	if err != nil {
		log.WithFields(logrus.Fields{
			"component": component,
			"error":     err,
			"cmd":       fmt.Sprintf("%s %s", encBin, strings.Join(cmdArgs, " ")),
			"output":    fmt.Sprintf("%s", out),
		}).Fatal("JSON Unmarshal command response returned error.")
	}

	endOfAssets = cmdResp.EndOfAssets

	if len(cmdResp.Data) == 0 {
		return []asset.Asset{}, endOfAssets
	}

	for serial, attributes := range cmdResp.Data {
		if len(attributes.IpAddress) == 0 {
			metric.IncrCounter([]string{"inventory", "assets_noip_enc"}, 1)
			continue
		}

		extras := e.AttributesExtrasAsMap(attributes.Extras)
		assets = append(assets,
			asset.Asset{IpAddresses: attributes.IpAddress,
				Serial:   serial,
				Type:     assetType,
				Location: attributes.Location,
				Extra:    extras,
			})
	}

	metric.IncrCounter([]string{"inventory", "assets_fetched_enc"}, float32(len(assets)))

	return assets, endOfAssets
}

// AssetIter fetches assets and sends them over the asset channel.
func (e *Enc) AssetIter() {

	//Asset needs to be an inventory asset
	//Iter stuffs assets into an array of Assets
	//Iter writes the assets array to the channel
	//component := "AssetIterEnc"

	//metric := d.MetricsEmitter

	defer close(e.AssetsChan)
	//defer d.MetricsEmitter.MeasureSince(component, time.Now())

	locations := strings.Join(e.Config.Locations, ",")
	for _, assetType := range e.FilterAssetType {

		var limit = e.BatchSize
		var offset = 0

		for {
			var endOfAssets bool

			assets, endOfAssets := e.encQueryByOffset(assetType, offset, limit, locations)

			//pass the asset to the channel
			e.AssetsChan <- assets

			//increment offset for next set of assets
			offset += limit

			//If the ENC indicates we've reached the end of assets
			if endOfAssets {
				break
			}
		} // endless for
	} // for each assetType
}

// AssetIterBySerial reads in list of serials passed in via cli,
// queries the ENC for the serials, passes them to the assets channel
func (e *Enc) AssetIterBySerial() {

	defer close(e.AssetsChan)

	//get serials passed in via cli - they need to be comma separated
	serials := e.Config.FilterParams.Serials

	//query ENC for given serials
	assets := e.encQueryBySerial(serials)

	//pass assets returned by ENC to the assets channel
	e.AssetsChan <- assets
}

// AssetIterByIp reads in list of ips passed in via cli,
// queries the ENC for attributes related to the, passes them to the assets channel
// if no attributes for a given IP are returned, an asset with just the IP is returned.
func (e *Enc) AssetIterByIp() {

	defer close(e.AssetsChan)

	//get ips passed in via cli - they need to be comma separated
	ips := e.Config.FilterParams.Ips

	//query ENC for given serials
	assets := e.encQueryByIp(ips)

	//pass assets returned by ENC to the assets channel
	e.AssetsChan <- assets
}