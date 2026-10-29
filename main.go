// Command lody-mygo is a native Lody client: MyGo draws its interface on the
// GPU, without a web view, and it syncs with Lody Cloud directly.
package main

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/lbr77/lody-mygo/internal/app"
)

func main() {
	// Not "Lody": the official desktop app owns that data directory.
	mygo.App.SetName("Lody Native")
	a := app.New()
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:                "Lody",
			Width:                1100,
			Height:               720,
			MinWidth:             640,
			MinHeight:            480,
			TitleBarStyle:        mygo.TitleBarHiddenInset,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 16},
			BackgroundColor:      "light-dark(#F0F0F0, #191A1D)",
			StateKey:             "main",
			Content:              a.View(),
		})
		a.Start(win)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
