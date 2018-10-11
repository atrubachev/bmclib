// Copyright © 2018 Joel Rebello <joel.rebello@booking.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package asset

type Asset struct {
	//A chassis asset may have more than one IPs,
	//when asset is first retrieved all IPs are listed in this slice.
	IpAddresses []string
	//The active IP is assigned to this field once identified,
	IpAddress string
	Serial    string
	Vendor    string
	Model     string
	Type      string //blade or chassis
	Location  string
	Setup     bool              //If setup is set, butlers will setup the asset.
	Configure bool              //If setup is set, butlers will configure the asset.
	Execute   bool              //If execute is set, butlers will execute given command(s) on the asset.
	Extra     map[string]string //any extra params needed to be set in a asset.

}
