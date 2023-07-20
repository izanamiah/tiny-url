package routes

import (
	"os"
	"strconv"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/izanamiah/tiny-url/database"
	"github.com/izanamiah/tiny-url/helpers"
	"github.com/redis/go-redis/v9"
)

type request struct{
	URL 			string			`json:"url"`
	CustomShort		string			`json:"short"`
	Expiry			time.Duration	`json:"expiry"`
}

type response struct{
	URL				string			`json:"url"`
	CustomShort		string			`json:"short"`
	Expiry			time.Duration	`json:"expiry"`
	XRateRemaining	int				`json:"rate_limit"`
	XRateLimitReset	time.Duration	`json:"rate_limit_reset"`
}

func ShortenURL( c *fiber.Ctx) error {
	body := new(request)

	if err := c.BodyParser(&body); err!= nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error":"cannot parse JSON",
		"message" : err,
	})
	}

	//rate limiting logic
	r2 := database.CreateClient(1)
	defer r2.Close()
	val, err := r2.Get(database.Ctx, c.IP()).Result()
	if err == redis.Nil{
		_ = r2.Set(database.Ctx, c.IP(), os.Getenv("API_QUOTA"), 30*60*time.Second).Err()
	} else {
		// the user address has been found
		// meaing this user has used service in the past 30min
		val, _ = r2.Get(database.Ctx,c.IP()).Result() //! note: this line is redundant
		valInt, _ := strconv.Atoi(val) // convert string into integer
		if valInt <= 0 {
			limit, _ := r2.TTL(database.Ctx, c.IP()).Result()
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
					"error":"Rate limit exceeded",
					"rate_limit_rest": limit / time.Nanosecond / time.Minute,
				})
		}
	}
	
	//check if the input is an actual url
	if !govalidator.IsURL(body.URL) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error":"Invalid URL"})
	}	

	//check for doamin error
	// users may abuse the shortener by shorting the domain `localhost:3000` itself
	// leading to a inifite loop, so don't accept the domain for shortening
	if !helpers.RemoveDomainError(body.URL){
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error":"nice try"})
	}

	//enforce SSL (https)
	body.URL = helpers.EnforceHTTP(body.URL)


	//custom short link logic
	//check if the user has send a custom link request
	var id string
	if body.CustomShort == ""{
		//if no requeset for custom shortID is found, generate a random ID
		id = uuid.New().String()[:6]
	} else {
		id = body.CustomShort
	}

	r := database.CreateClient(0)
	defer r.Close()

	//check if the url already exist in db
	val, _ = r.Get(database.Ctx, id).Result()
	if val != "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error" : "Custom url short is already in use",
		})
	}

	if body.Expiry == 0 {
		body.Expiry = 24
	}

	err = r.Set(database.Ctx, id, body.URL, body.Expiry*3600*time.Second).Err()

	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error" :"Unable to connect to server",
		})
	}

	//generate response
	resp := response {
		URL:			body.URL,
		CustomShort:	"",
		Expiry: 		body.Expiry,
		XRateRemaining:	10,
		XRateLimitReset: 30,
	}

	r2.Decr(database.Ctx, c.IP())

	val, _ = r2.Get(database.Ctx, c.IP()).Result()
	resp.XRateRemaining, _ = strconv.Atoi(val)

	ttl, _ := r2.TTL(database.Ctx, c.IP()).Result()
	resp.XRateLimitReset = ttl / time.Nanosecond / time.Minute

	resp. CustomShort = os.Getenv("DOMAIN") + "/" + id

	return c.Status(fiber.StatusOK).JSON(resp)
}